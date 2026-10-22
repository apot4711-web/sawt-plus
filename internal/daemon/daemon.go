// Package daemon owns the audio pipeline: discovery, pairing, the Android
// connection, PipeWire feeding, and event fan-out.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/apot4711-web/sawt-plus/internal/adb"
	"github.com/apot4711-web/sawt-plus/internal/audio"
	"github.com/apot4711-web/sawt-plus/internal/config"
	"github.com/apot4711-web/sawt-plus/internal/discovery"
	"github.com/apot4711-web/sawt-plus/internal/protocol"
	"github.com/apot4711-web/sawt-plus/internal/version"
)

// audioSource is the subset of audio.Source used by the daemon. It is an
// interface to keep the pipeline testable.
type audioSource interface {
	Write(pcm []byte)
	Stats() (written, dropped uint64, readers int32)
	Name() string
	Close() error
}

type State string

const (
	StateIdle        State = "idle"
	StateDiscovering State = "discovering"
	StatePairing     State = "pairing"
	StateConnecting  State = "connecting"
	StateStreaming   State = "streaming"
	StateError       State = "error"
)

var (
	ErrPairRequired = errors.New("device requires pairing")
	ErrNoDevice     = errors.New("no device selected")
	ErrClosed       = errors.New("daemon is shut down")
)

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Status struct {
	State       State     `json:"state"`
	Device      string    `json:"device"`
	DeviceID    string    `json:"device_id"`
	Address     string    `json:"address"`
	Transport   string    `json:"transport"`
	Source      string    `json:"source"`
	Rate        int       `json:"rate"`
	Channels    int       `json:"channels"`
	FrameMS     int       `json:"frame_ms"`
	Clients     int       `json:"clients"`
	RTTMS       int64     `json:"rtt_ms"`
	Frames      uint64    `json:"frames"`
	Bytes       uint64    `json:"bytes"`
	Dropped     uint64    `json:"dropped"`
	Gain        float64   `json:"gain"`
	Mute        bool      `json:"mute"`
	Since       time.Time `json:"since"`
	LastFrameAt time.Time `json:"last_frame_at"`
	Active      bool      `json:"active"`
	Error       string    `json:"error,omitempty"`
}

type Event struct {
	Type    string             `json:"type"`
	Status  Status             `json:"status"`
	Log     *LogEntry          `json:"log,omitempty"`
	Devices []discovery.Device `json:"devices,omitempty"`
}

type Daemon struct {
	cfg     *config.Config
	cfgPath string

	cfgMu sync.RWMutex

	mu         sync.RWMutex
	status     Status
	source     audioSource
	logs       []LogEntry
	closed     bool
	lastTarget string

	subs  map[chan Event]struct{}
	feeds map[chan []byte]struct{}

	baseCtx context.Context
	cancel  context.CancelFunc

	connMu       sync.Mutex
	ws           *websocket.Conn
	streamCancel context.CancelFunc
	streamMu     sync.Mutex
	wg           sync.WaitGroup

	frames  atomic.Uint64
	bytes   atomic.Uint64
	dropped atomic.Uint64
	rtt     atomic.Int64
}

func New(cfg *config.Config) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	return &Daemon{
		cfg:     cfg,
		cfgPath: config.Path(),
		status: Status{
			State:    StateIdle,
			Source:   cfg.Audio.SourceName,
			Rate:     cfg.Audio.Rate,
			Channels: cfg.Audio.Channels,
			FrameMS:  cfg.Audio.FrameMS,
			Since:    now,
		},
		subs:    make(map[chan Event]struct{}),
		feeds:   make(map[chan []byte]struct{}),
		baseCtx: ctx,
		cancel:  cancel,
	}
}

// Config returns a snapshot of the configuration. Callers must not keep the
// pointer, and secrets (device tokens) are included only for internal use.
func (d *Daemon) Config() config.Config {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	out := *d.cfg
	out.Devices = append([]config.DeviceEntry(nil), d.cfg.Devices...)
	return out
}

// SavedDevice is a device entry without the pairing token, safe to expose to
// the browser interface.
type SavedDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Active  bool   `json:"active"`
}

// activeWindow is how long a device keeps counting as active after its last
// PCM frame. It tolerates small gaps (jitter, scheduling) without keeping the
// UI marked active long after the stream stopped.
const activeWindow = 3 * time.Second

func (d *Daemon) SavedDevices() []SavedDevice {
	// Status() snapshots the current device and active window before taking the
	// config lock, so the two locks are never held at the same time.
	st := d.Status()
	activeAddr := config.NormalizeAddress(st.Address)

	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	out := make([]SavedDevice, 0, len(d.cfg.Devices))
	for _, dev := range d.cfg.Devices {
		active := st.Active &&
			((dev.ID != "" && dev.ID == st.DeviceID) ||
				(activeAddr != "" && config.NormalizeAddress(dev.Address) == activeAddr))
		out = append(out, SavedDevice{
			ID:      dev.ID,
			Name:    dev.Name,
			Address: dev.Address,
			Active:  active,
		})
	}
	return out
}

func (d *Daemon) Context() context.Context { return d.baseCtx }

func (d *Daemon) Status() Status {
	d.cfgMu.RLock()
	gain := d.cfg.Audio.Gain
	mute := d.cfg.Audio.Mute
	d.cfgMu.RUnlock()

	d.mu.RLock()
	s := d.status
	src := d.source
	d.mu.RUnlock()
	s.Gain = gain
	s.Mute = mute
	s.Frames = d.frames.Load()
	s.Bytes = d.bytes.Load()
	s.Dropped = d.dropped.Load()
	if src != nil {
		_, dropped, _ := src.Stats()
		s.Dropped += dropped
	}
	s.RTTMS = d.rtt.Load()
	s.Active = !s.LastFrameAt.IsZero() && time.Since(s.LastFrameAt) <= activeWindow
	return s
}

func (d *Daemon) Logs() []LogEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]LogEntry, len(d.logs))
	copy(out, d.logs)
	return out
}

func (d *Daemon) Log(level, format string, args ...any) {
	entry := LogEntry{Time: time.Now(), Level: level, Message: fmt.Sprintf(format, args...)}
	d.mu.Lock()
	d.logs = append(d.logs, entry)
	if len(d.logs) > 300 {
		d.logs = d.logs[len(d.logs)-300:]
	}
	d.mu.Unlock()
	d.broadcast(Event{Type: "log", Status: d.Status(), Log: &entry})
}

func (d *Daemon) setState(state State, err error) {
	d.mu.Lock()
	d.status.State = state
	d.status.Since = time.Now()
	if err != nil {
		d.status.Error = err.Error()
	} else {
		d.status.Error = ""
	}
	d.mu.Unlock()
	d.broadcastStatus()
}

func (d *Daemon) broadcastStatus() {
	d.broadcast(Event{Type: "status", Status: d.Status()})
}

func (d *Daemon) broadcast(ev Event) {
	// Sends are non-blocking, so holding the read lock for the duration keeps
	// unsubscribe/Shutdown (which need the write lock to close a channel) from
	// closing a channel between the lookup and the send.
	d.mu.RLock()
	defer d.mu.RUnlock()
	for ch := range d.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (d *Daemon) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	d.subs[ch] = struct{}{}
	d.mu.Unlock()
	return ch, func() {
		// Unregister only. Closing the channel here would race with broadcast,
		// which may already be sending on it. Shutdown closes every remaining
		// channel after all producers have stopped.
		d.mu.Lock()
		delete(d.subs, ch)
		d.mu.Unlock()
	}
}

// Feed returns a channel of PCM frames for the browser or other consumers.
func (d *Daemon) Feed() (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	d.feeds[ch] = struct{}{}
	d.mu.Unlock()
	return ch, func() {
		d.mu.Lock()
		delete(d.feeds, ch)
		d.mu.Unlock()
	}
}

func (d *Daemon) broadcastPCM(pcm []byte) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for ch := range d.feeds {
		frame := append([]byte(nil), pcm...)
		select {
		case ch <- frame:
		default:
			d.dropped.Add(1)
		}
	}
}

func (d *Daemon) Discover(ctx context.Context, timeout time.Duration) ([]discovery.Device, error) {
	// Discovery must not disturb an active stream: the browser UI can scan
	// for other devices while audio is playing.
	d.mu.RLock()
	streaming := d.status.State == StateStreaming
	d.mu.RUnlock()
	if !streaming {
		d.setState(StateDiscovering, nil)
	}
	adbCh := make(chan []discovery.Device, 1)
	go func() { adbCh <- d.adbDevices(ctx) }()

	devices, err := discovery.Browse(ctx, timeout)
	adbDevices := <-adbCh
	devices = append(devices, adbDevices...)
	if err != nil && len(devices) == 0 {
		if !streaming {
			d.setState(StateError, err)
		}
		return nil, err
	}
	if err != nil {
		d.Log("warn", "network discovery: %v", err)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Label() < devices[j].Label() })
	d.broadcast(Event{Type: "devices", Status: d.Status(), Devices: devices})
	if !streaming {
		d.setState(StateIdle, nil)
	}
	return devices, nil
}

func (d *Daemon) adbDevices(ctx context.Context) []discovery.Device {
	if !adb.Available() {
		return nil
	}
	scanCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	list, err := adb.New("").Devices(scanCtx)
	if err != nil {
		return nil
	}
	devices := make([]discovery.Device, 0, len(list))
	for _, dev := range list {
		if dev.State != "device" {
			continue
		}
		name := firstNonEmpty(dev.Model, dev.Serial)
		devices = append(devices, discovery.Device{
			ID:        "adb:" + dev.Serial,
			Name:      name,
			Host:      dev.Serial,
			Address:   "adb:" + dev.Serial,
			Version:   "adb",
			Transport: "adb",
		})
	}
	return devices
}

func (d *Daemon) ensureSource(ctx context.Context) (audioSource, error) {
	d.mu.Lock()
	if d.source != nil {
		src := d.source
		d.mu.Unlock()
		return src, nil
	}
	d.mu.Unlock()

	d.cfgMu.RLock()
	cfg := d.cfg.Audio
	d.cfgMu.RUnlock()

	src, err := audio.NewSource(cfg)
	if err != nil {
		return nil, err
	}
	if err := src.Start(ctx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.source == nil {
		d.source = src
		d.status.Source = src.Name()
		d.status.Rate = cfg.Rate
		d.status.Channels = cfg.Channels
		d.status.FrameMS = cfg.FrameMS
	}
	d.mu.Unlock()
	d.Log("info", "PipeWire source ready: %s", src.Name())
	return src, nil
}

func (d *Daemon) closeSource() {
	d.mu.Lock()
	src := d.source
	d.source = nil
	d.mu.Unlock()
	if src != nil {
		if err := src.Close(); err != nil {
			d.Log("warn", "close source: %v", err)
		}
	}
}

func (d *Daemon) SetGain(gain float64) {
	if gain < 0 {
		gain = 0
	}
	if gain > 8 {
		gain = 8
	}
	d.cfgMu.Lock()
	d.cfg.Audio.Gain = gain
	d.cfgMu.Unlock()
	d.Log("info", "gain set to %.2fx", gain)
	d.broadcastStatus()
}

func (d *Daemon) SetMute(mute bool) {
	d.cfgMu.Lock()
	d.cfg.Audio.Mute = mute
	d.cfgMu.Unlock()
	if mute {
		d.Log("info", "microphone muted")
	} else {
		d.Log("info", "microphone unmuted")
	}
	d.broadcastStatus()
}

func (d *Daemon) ResolveDevice(target string) (config.DeviceEntry, string, error) {
	d.mu.RLock()
	last := d.lastTarget
	d.mu.RUnlock()

	if target == "" {
		target = last
	}
	if target == "" {
		saved := d.SavedDevices()
		if len(saved) == 1 {
			target = saved[0].Address
		}
	}
	if target == "" {
		return config.DeviceEntry{}, "", ErrNoDevice
	}
	if strings.HasPrefix(target, "adb:") || target == "adb" {
		serial := strings.TrimPrefix(target, "adb:")
		return config.DeviceEntry{Address: target, Name: "ADB " + serial, ID: target}, "adb", nil
	}
	addr := config.NormalizeAddress(target)
	d.cfgMu.RLock()
	entry, ok := d.cfg.FindDevice(target)
	if !ok {
		entry, ok = d.cfg.FindDevice(addr)
	}
	d.cfgMu.RUnlock()
	if !ok {
		return config.DeviceEntry{Address: addr}, "wifi", nil
	}
	// A saved device may be addressed by name or ID. Only replace its stored
	// address when the target itself is a network address, otherwise a name
	// such as "Phone A" would be normalized into "PhoneA:8765".
	if strings.Contains(target, ":") {
		entry.Address = addr
	}
	return entry, "wifi", nil
}

func (d *Daemon) StartStream(target string) error {
	entry, transport, err := d.ResolveDevice(target)
	if err != nil {
		return err
	}

	// Serialize stream starts so two concurrent requests cannot overwrite each
	// other's streamCancel and leave an uncancelled reconnect loop behind.
	d.streamMu.Lock()
	defer d.streamMu.Unlock()

	d.Disconnect()

	ctx, cancel := context.WithCancel(d.baseCtx)

	// Add to the wait group under d.mu: Shutdown sets closed under the same
	// lock before waiting, so Add can never race with Wait.
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		cancel()
		return ErrClosed
	}
	d.lastTarget = entry.Address
	if transport != "adb" {
		d.status.Device = entry.Name
		d.status.DeviceID = entry.ID
		d.status.Address = entry.Address
		d.status.Transport = "wifi"
	}
	d.wg.Add(1)
	d.mu.Unlock()

	d.connMu.Lock()
	d.streamCancel = cancel
	d.connMu.Unlock()

	if transport == "adb" {
		go func() {
			defer d.wg.Done()
			d.adbLoop(ctx, entry.Address)
		}()
		return nil
	}

	go func() {
		defer d.wg.Done()
		d.wifiLoop(ctx, entry)
	}()
	return nil
}

func (d *Daemon) Disconnect() {
	d.connMu.Lock()
	if d.streamCancel != nil {
		d.streamCancel()
		d.streamCancel = nil
	}
	ws := d.ws
	d.ws = nil
	d.connMu.Unlock()

	if ws != nil {
		_ = ws.Close()
	}
	d.setState(StateIdle, nil)
}

func (d *Daemon) wifiLoop(ctx context.Context, entry config.DeviceEntry) {
	backoff := time.Second
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := d.streamWiFi(ctx, entry)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrPairRequired) {
			d.Log("warn", "pairing required for %s; use the pair command", entry.Address)
			d.setState(StatePairing, err)
			return
		}
		if err != nil {
			failures++
			d.Log("error", "%v", err)
			d.setState(StateError, err)
		} else {
			failures = 0
		}
		if !d.Config().Daemon.Reconnect {
			return
		}
		// A changed DHCP lease leaves the saved IP stale. After a few failed
		// attempts, look the phone up again by ID/name and retry immediately.
		if failures >= 3 {
			if d.rediscover(ctx, &entry) {
				failures = 0
				backoff = time.Second
				continue
			}
		}
		d.Log("info", "reconnecting in %s", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

// rediscover refreshes a saved device's address after the phone's DHCP lease
// changes. It matches by stable ID first and by name as a fallback.
func (d *Daemon) rediscover(ctx context.Context, entry *config.DeviceEntry) bool {
	if entry.ID == "" && entry.Name == "" {
		return false
	}
	browseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	devices, err := discovery.Browse(browseCtx, 3*time.Second)
	if err != nil && len(devices) == 0 {
		return false
	}
	for _, dev := range devices {
		if dev.Transport == "adb" || dev.Address == "" {
			continue
		}
		matched := (entry.ID != "" && dev.ID == entry.ID) ||
			(entry.Name != "" && dev.Name == entry.Name)
		if !matched {
			continue
		}
		addr := config.NormalizeAddress(dev.Address)
		if addr == "" || addr == entry.Address {
			return false
		}
		oldAddr := entry.Address
		entry.Address = addr
		entry.ID = firstNonEmpty(dev.ID, entry.ID)
		entry.Name = firstNonEmpty(entry.Name, dev.Name)

		d.cfgMu.Lock()
		d.cfg.RemoveDevice(oldAddr)
		d.cfg.UpsertDevice(*entry)
		saveErr := d.cfg.Save()
		d.cfgMu.Unlock()
		if saveErr != nil {
			d.Log("warn", "save rediscovered address: %v", saveErr)
		}
		d.Log("info", "rediscovered %s at %s", firstNonEmpty(entry.Name, entry.ID), addr)
		return true
	}
	return false
}

func (d *Daemon) streamWiFi(ctx context.Context, entry config.DeviceEntry) error {
	d.setState(StateConnecting, nil)
	addr := entry.Address
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	url := "ws://" + addr + "/"
	ws, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	ws.SetReadLimit(16 << 20)
	_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))

	d.connMu.Lock()
	d.ws = ws
	d.connMu.Unlock()
	defer func() {
		d.connMu.Lock()
		if d.ws == ws {
			d.ws = nil
		}
		d.connMu.Unlock()
		_ = ws.Close()
	}()

	hello, _ := protocol.Encode(protocol.Hello(entry.Token, version.UserAgent))
	if err := ws.WriteMessage(websocket.TextMessage, hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	msg, err := d.readControl(ws, 10*time.Second)
	if err != nil {
		return err
	}
	switch msg.Type {
	case "pair_required":
		return ErrPairRequired
	case "error":
		return fmt.Errorf("device error: %s", msg.Error)
	case "ready":
	default:
		return fmt.Errorf("unexpected handshake message %q", msg.Type)
	}

	entry.Name = firstNonEmpty(msg.Name, entry.Name, addr)
	entry.ID = firstNonEmpty(msg.ID, entry.ID)
	entry.Address = addr

	rate, channels, frameMS := msg.Rate, msg.Channels, msg.FrameMS
	d.cfgMu.Lock()
	d.cfg.UpsertDevice(entry)
	if rate <= 0 {
		rate = d.cfg.Audio.Rate
	}
	if channels <= 0 {
		channels = d.cfg.Audio.Channels
	}
	if frameMS <= 0 {
		frameMS = d.cfg.Audio.FrameMS
	}
	d.cfg.Audio.Rate = rate
	d.cfg.Audio.Channels = channels
	d.cfg.Audio.FrameMS = frameMS
	saveErr := d.cfg.Save()
	d.cfgMu.Unlock()
	if saveErr != nil {
		d.Log("warn", "save config: %v", saveErr)
	}

	d.mu.Lock()
	d.status.Device = entry.Name
	d.status.DeviceID = entry.ID
	d.status.Address = entry.Address
	d.status.Transport = "wifi"
	d.status.Clients = firstNonZero(msg.Clients, 1)
	d.status.Rate = rate
	d.status.Channels = channels
	d.status.FrameMS = frameMS
	d.mu.Unlock()

	if _, err := d.ensureSource(ctx); err != nil {
		return err
	}
	d.setState(StateStreaming, nil)
	d.Log("info", "streaming from %s (%d Hz, %d ch)", entry.Name, rate, channels)

	pingCtx, pingCancel := context.WithCancel(ctx)
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		d.pingLoop(pingCtx, ws)
	}()
	defer func() {
		pingCancel()
		<-pingDone
	}()

	return d.readStream(ctx, ws)
}

func (d *Daemon) pingLoop(ctx context.Context, ws *websocket.Conn) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msg, _ := protocol.Encode(protocol.Ping(time.Now().UnixMilli()))
			_ = ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

func (d *Daemon) readControl(ws *websocket.Conn, timeout time.Duration) (protocol.Message, error) {
	_ = ws.SetReadDeadline(time.Now().Add(timeout))
	for {
		typ, data, err := ws.ReadMessage()
		if err != nil {
			return protocol.Message{}, fmt.Errorf("read handshake: %w", err)
		}
		if typ != websocket.TextMessage {
			continue
		}
		msg, err := protocol.Decode(data)
		if err != nil {
			return protocol.Message{}, err
		}
		return msg, nil
	}
}

func (d *Daemon) readStream(ctx context.Context, ws *websocket.Conn) error {
	_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		typ, data, err := ws.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("stream closed: %w", err)
		}
		_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		switch typ {
		case websocket.BinaryMessage:
			d.ProcessPCM(data)
		case websocket.TextMessage:
			msg, err := protocol.Decode(data)
			if err != nil {
				continue
			}
			d.handleControl(msg)
		}
	}
}

func (d *Daemon) handleControl(msg protocol.Message) {
	switch msg.Type {
	case "pong":
		if msg.T > 0 {
			rtt := time.Now().UnixMilli() - msg.T
			if rtt >= 0 {
				d.rtt.Store(rtt)
			}
		}
	case "stats":
		if msg.RTT > 0 {
			d.rtt.Store(msg.RTT)
		}
		if msg.Clients >= 0 {
			d.mu.Lock()
			d.status.Clients = msg.Clients
			d.mu.Unlock()
		}
		d.broadcastStatus()
	case "error":
		if msg.Error != "" {
			d.Log("error", "device: %s", msg.Error)
		}
	}
}

func (d *Daemon) ProcessPCM(pcm []byte) {
	if len(pcm) == 0 {
		return
	}
	d.mu.Lock()
	d.status.LastFrameAt = time.Now()
	d.mu.Unlock()

	d.cfgMu.RLock()
	gain := d.cfg.Audio.Gain
	mute := d.cfg.Audio.Mute
	d.cfgMu.RUnlock()

	d.mu.RLock()
	src := d.source
	d.mu.RUnlock()

	if mute {
		for i := range pcm {
			pcm[i] = 0
		}
	} else {
		audio.ApplyGain(pcm, gain)
	}

	if src != nil {
		src.Write(pcm)
	}
	d.broadcastPCM(pcm)
	d.frames.Add(1)
	d.bytes.Add(uint64(len(pcm)))
	d.broadcastStatus()
}

// Unpair removes a saved Wi-Fi pairing (and its token), stopping the stream
// first when the device is currently connected. ADB devices are not paired.
func (d *Daemon) Unpair(target string) error {
	entry, transport, err := d.ResolveDevice(target)
	if err != nil {
		return err
	}
	if transport == "adb" {
		return errors.New("ADB devices are not paired; use disconnect instead")
	}

	d.mu.RLock()
	connected := d.status.Address == entry.Address ||
		(entry.ID != "" && d.status.DeviceID == entry.ID)
	d.mu.RUnlock()
	if connected {
		d.Disconnect()
	}

	d.cfgMu.Lock()
	removed := d.cfg.RemoveDevice(entry.Address)
	if entry.ID != "" {
		removed = d.cfg.RemoveDevice(entry.ID) || removed
	}
	if entry.Name != "" {
		removed = d.cfg.RemoveDevice(entry.Name) || removed
	}
	if !removed {
		d.cfgMu.Unlock()
		return fmt.Errorf("no saved pairing for %s", entry.Address)
	}
	err = d.cfg.Save()
	d.cfgMu.Unlock()
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	d.mu.Lock()
	if d.status.Address == entry.Address || (entry.ID != "" && d.status.DeviceID == entry.ID) {
		d.status.Device = ""
		d.status.DeviceID = ""
		d.status.Address = ""
	}
	d.mu.Unlock()
	d.Log("info", "unpaired %s", entry.Address)
	return nil
}

func (d *Daemon) Pair(ctx context.Context, target, code string) (config.DeviceEntry, error) {
	entry, transport, err := d.ResolveDevice(target)
	if err != nil {
		return entry, err
	}
	if transport == "adb" {
		return entry, errors.New("ADB devices do not use pairing codes; run: sawt adb pair HOST:PORT CODE")
	}
	// Stop any previous connection or reconnect loop before pairing a new
	// device, otherwise the old loop keeps retrying in the background.
	d.Disconnect()

	addr := entry.Address
	d.setState(StatePairing, nil)
	d.Log("info", "pairing with %s", addr)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws, _, err := dialer.DialContext(ctx, "ws://"+addr+"/", nil)
	if err != nil {
		d.setState(StateError, err)
		d.Log("warn", "pairing failed: %v", err)
		return entry, fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))

	msg, _ := protocol.Encode(protocol.Pair(code, version.UserAgent))
	if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
		d.Log("warn", "pairing failed: %v", err)
		return entry, err
	}
	for {
		typ, data, err := ws.ReadMessage()
		if err != nil {
			d.setState(StateError, err)
			d.Log("warn", "pairing failed: %v", err)
			return entry, fmt.Errorf("pairing failed: %w", err)
		}
		if typ != websocket.TextMessage {
			continue
		}
		reply, err := protocol.Decode(data)
		if err != nil {
			continue
		}
		if reply.Type == "error" {
			err := fmt.Errorf("pairing rejected: %s", reply.Error)
			d.setState(StateError, err)
			d.Log("warn", "pairing failed: %v", err)
			return entry, err
		}
		if reply.Type != "paired" {
			continue
		}
		entry.Token = reply.Token
		entry.Name = firstNonEmpty(reply.Name, entry.Name, addr)
		entry.ID = firstNonEmpty(reply.ID, entry.ID)
		entry.Address = addr
		d.cfgMu.Lock()
		d.cfg.UpsertDevice(entry)
		saveErr := d.cfg.Save()
		d.cfgMu.Unlock()
		if saveErr != nil {
			return entry, fmt.Errorf("save config: %w", saveErr)
		}
		d.setState(StateIdle, nil)
		d.Log("info", "paired with %s", entry.Name)
		return entry, nil
	}
}

func (d *Daemon) adbLoop(ctx context.Context, target string) {
	serial := strings.TrimPrefix(target, "adb:")
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := d.streamADB(ctx, serial)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			d.Log("error", "adb: %v", err)
			d.setState(StateError, err)
		}
		if !d.Config().Daemon.Reconnect {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

func (d *Daemon) streamADB(ctx context.Context, serial string) error {
	if !adb.Available() {
		return errors.New("adb not found")
	}
	m := adb.New(serial)
	d.setState(StateConnecting, nil)
	d.Log("info", "waiting for ADB device %s", firstNonEmpty(serial, "(default)"))
	if err := m.Wait(ctx); err != nil {
		return err
	}
	if err := m.Grant(ctx, "com.sawtplus.app"); err != nil {
		d.Log("warn", "permissions: %v", err)
	}
	if err := m.StartApp(ctx, "com.sawtplus.app"); err != nil {
		return err
	}
	port, err := m.Forward(ctx, "localabstract:sawt-plus")
	if err != nil {
		return err
	}
	defer m.RemoveForward(context.Background(), port)

	if _, err := d.ensureSource(ctx); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	d.setState(StateStreaming, nil)
	d.mu.Lock()
	d.status.Transport = "adb"
	d.status.Address = "adb:" + serial
	d.status.Device = firstNonEmpty(serial, "Android device")
	d.status.Clients = 1
	d.mu.Unlock()
	d.Log("info", "streaming over ADB from %s", firstNonEmpty(serial, "device"))

	frameSize := d.Config().FrameBytes()
	if frameSize <= 0 {
		frameSize = 3840
	}
	buf := make([]byte, frameSize)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := io.ReadFull(conn, buf)
		if n > 0 {
			d.ProcessPCM(buf[:n])
		}
		if err != nil {
			return fmt.Errorf("adb stream: %w", err)
		}
	}
}

// StartAutoConnect connects to the first saved device with a token.
func (d *Daemon) StartAutoConnect() {
	cfg := d.Config()
	if !cfg.Daemon.AutoConnect {
		return
	}
	for _, dev := range cfg.Devices {
		if dev.Token != "" {
			d.Log("info", "auto-connecting to %s", firstNonEmpty(dev.Name, dev.Address))
			if err := d.StartStream(dev.Address); err != nil {
				d.Log("warn", "auto-connect: %v", err)
			}
			return
		}
	}
}

func (d *Daemon) Shutdown() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	d.mu.Unlock()

	d.Disconnect()
	d.cancel()
	d.wg.Wait()
	d.closeSource()

	d.mu.Lock()
	for ch := range d.subs {
		delete(d.subs, ch)
		close(ch)
	}
	for ch := range d.feeds {
		delete(d.feeds, ch)
		close(ch)
	}
	d.mu.Unlock()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// SaveConfig persists the current configuration.
func (d *Daemon) SaveConfig() error {
	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	return d.cfg.Save()
}

// UpdateConfig applies user-editable settings. Pairing tokens and the saved
// device list are intentionally preserved: the API never accepts secrets.
func (d *Daemon) UpdateConfig(incoming config.Config) error {
	incoming.Devices = nil
	incoming.Normalize()

	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	if incoming.Daemon.WebAddr != d.cfg.Daemon.WebAddr {
		return errors.New("web_addr cannot be changed while the daemon is running; edit config.toml and restart the daemon")
	}
	d.cfg.Audio = incoming.Audio
	d.cfg.Daemon.AutoConnect = incoming.Daemon.AutoConnect
	d.cfg.Daemon.Reconnect = incoming.Daemon.Reconnect
	d.cfg.UI = incoming.UI
	return d.cfg.Save()
}

func (d *Daemon) ConfigPath() string { return config.Path() }

func init() {
	// Make sure a missing config directory does not break early logging.
	_ = os.MkdirAll(config.Dir(), 0o700)
}
