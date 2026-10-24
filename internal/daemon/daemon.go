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

	mu     sync.RWMutex
	status Status
	// lastStatusBroadcast is the time of the most recent "status" event push.
	// It lets the hot audio path throttle broadcasts (see
	// broadcastStatusThrottled) while state transitions still push immediately.
	lastStatusBroadcast time.Time
	source              audioSource
	logs                []LogEntry
	closed              bool
	lastTarget          string
	// pairThrottle maps a normalized target address to its pairing-attempt
	// throttle state. Guarded by mu.
	pairThrottle map[string]*pairThrottleState

	subs  map[chan Event]struct{}
	feeds map[chan []byte]struct{}

	baseCtx context.Context
	cancel  context.CancelFunc

	connMu       sync.Mutex
	ws           *websocket.Conn
	streamCancel context.CancelFunc
	streamMu     sync.Mutex
	wg           sync.WaitGroup

	// adbRunningHint makes the "app already running, not relaunching" notice
	// appear once per daemon instead of on every ADB retry.
	adbRunningHint sync.Once
	// powerSaveWarned limits the host Wi-Fi power-save warning to once per
	// daemon process.
	powerSaveWarned atomic.Bool

	frames  atomic.Uint64
	bytes   atomic.Uint64
	dropped atomic.Uint64
	rtt     atomic.Int64
}

func New(cfg *config.Config) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	d := &Daemon{
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
	// USB hotplug auto-connect for previously-paired phones. Silent no-op
	// when adb is not installed.
	d.startADBHotplug()
	return d
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

// statusPushInterval is the minimum interval between status broadcasts
// triggered by the audio path. ProcessPCM runs ~50 times per second; taking a
// full Status snapshot and fanning it out to every SSE/WebSocket subscriber on
// each frame wastes CPU and floods subscribers, so the audio path throttles to
// this interval. State transitions (setState) and user actions (SetGain,
// SetMute) still broadcast immediately via broadcastStatus.
const statusPushInterval = 500 * time.Millisecond

// Inbound WebSocket sizing. The handshake exchanges only small JSON control
// messages, while the audio stream carries s16le PCM frames whose size the
// peer announces in "ready" (rate*frameMS/1000*channels*2 bytes; 48000 Hz
// mono 20 ms = 1920 B). Reads are capped at a small multiple of the
// negotiated frame with an absolute ceiling, so a rogue peer cannot force
// the multi-megabyte ApplyGain/broadcast copies the old 16 MB limit allowed.
const (
	wsHandshakeReadBytes = 64 << 10  // 64 KiB: handshake is JSON only
	wsReadLimitFactor    = 4         // headroom over the negotiated frame
	maxWSReadBytes       = 256 << 10 // 256 KiB: absolute ceiling
)

// Pairing-code brute-force protection. The code is 6 digits (10^6
// combinations) and each attempt is one LAN round-trip, so without
// client-side throttling an exhaustive search is feasible in hours. These
// bounds make guessing infeasible while staying invisible to a legitimate
// user (the first attempt is never delayed).
const (
	pairBaseBackoff = 2 * time.Second
	pairMaxBackoff  = 60 * time.Second
	pairMaxFails    = 8
	pairLockout     = 5 * time.Minute
)

// pairThrottleState tracks consecutive rejected pairing attempts against one
// normalized target address. Guarded by Daemon.mu. Only explicit server
// rejections ("error" replies, i.e. wrong codes) are counted — transport
// errors never throttle, so a network blip can't lock a user out.
type pairThrottleState struct {
	fails        int           // consecutive rejections
	backoff      time.Duration // delay enforced before the next attempt
	blockedUntil time.Time     // no attempts before this time
}

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
	d.mu.Lock()
	d.lastStatusBroadcast = time.Now()
	d.mu.Unlock()
	d.broadcast(Event{Type: "status", Status: d.Status()})
}

// broadcastStatusThrottled pushes a status event at most once per
// statusPushInterval. It is used on the hot audio path (ProcessPCM); the
// timestamp is refreshed by every broadcastStatus call, so a recent state
// transition also suppresses a redundant audio-path push.
func (d *Daemon) broadcastStatusThrottled() {
	d.mu.RLock()
	recent := time.Since(d.lastStatusBroadcast) < statusPushInterval
	d.mu.RUnlock()
	if recent {
		return
	}
	d.broadcastStatus()
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
	list, restarted, err := adb.DevicesFresh(scanCtx)
	if restarted {
		d.Log("warn", "adb server was stale (USB ADB interface present but no devices); restarted it")
	}
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
// changes. mDNS is unauthenticated, so a discovered name or ID only *selects*
// a candidate — the candidate is adopted only after it proves its identity by
// presenting the pinned TLS certificate during a tokenless handshake. A
// spoofed mDNS entry can therefore never become the saved device, and the
// pairing token is never transmitted to it.
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
	return d.adoptRediscoveredAddress(browseCtx, entry, devices, d.verifyRediscoverCandidate)
}

// adoptRediscoveredAddress scans mDNS devices for the saved entry (stable ID
// first, name as fallback) and adopts the first candidate whose identity
// verify confirms. verify must perform no token transmission.
func (d *Daemon) adoptRediscoveredAddress(ctx context.Context, entry *config.DeviceEntry, devices []discovery.Device, verify func(context.Context, *config.DeviceEntry, string) bool) bool {
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
			continue
		}
		// Identity check: the certificate fingerprint is the device's
		// identity, not its mDNS name or address (both spoofable).
		if !verify(ctx, entry, addr) {
			continue
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

// verifyRediscoverCandidate reports whether the endpoint at addr proves it is
// the saved device by presenting the pinned TLS certificate. The handshake
// is tokenless: dialWSS aborts inside the TLS handshake on a fingerprint
// mismatch, before any application data — so a spoofed mDNS entry can never
// learn the pairing token. Fail closed: with no pinned certificate (paired
// before TLS existed) there is nothing to verify against, so mDNS is never
// followed; re-pair with a new 6-digit code to establish the pin.
func (d *Daemon) verifyRediscoverCandidate(ctx context.Context, entry *config.DeviceEntry, addr string) bool {
	if entry.CertSHA256 == "" {
		d.Log("warn", "rediscover: no pinned certificate for %s; refusing to follow mDNS to %s (re-pair to establish identity)",
			firstNonEmpty(entry.Name, entry.ID), addr)
		return false
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ws, _, err := d.dialWSS(verifyCtx, addr, entry.CertSHA256)
	if err != nil {
		d.Log("warn", "rediscover: %s failed identity check: %v", addr, err)
		return false
	}
	_ = ws.Close()
	return true
}

func (d *Daemon) streamWiFi(ctx context.Context, entry config.DeviceEntry) error {
	d.setState(StateConnecting, nil)
	if !d.powerSaveWarned.Swap(true) {
		if warning := WifiPowerSaveWarning(); warning != "" {
			d.Log("warn", "%s", warning)
		}
	}
	addr := entry.Address
	// wss:// only, with certificate pinning. On a fingerprint mismatch the
	// handshake aborts here — the pairing token is never sent.
	ws, observedFP, err := d.dialWSS(ctx, addr, entry.CertSHA256)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	// Handshake phase: only small JSON control messages flow here.
	ws.SetReadLimit(wsHandshakeReadBytes)
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
		if err := checkAdvertisedFP(msg.CertSHA256, observedFP); err != nil {
			return err
		}
		return ErrPairRequired
	case "error":
		return fmt.Errorf("device error: %s", msg.Error)
	case "ready":
	default:
		return fmt.Errorf("unexpected handshake message %q", msg.Type)
	}

	if err := checkAdvertisedFP(msg.CertSHA256, observedFP); err != nil {
		return err
	}
	entry.Name = firstNonEmpty(msg.Name, entry.Name, addr)
	entry.ID = firstNonEmpty(msg.ID, entry.ID)
	entry.Address = addr
	// Upgrade path: devices paired before TLS existed have no pin yet. Pin
	// the presented certificate now (TOFU) so the next connect enforces it.
	if entry.CertSHA256 == "" {
		entry.CertSHA256 = observedFP
		d.Log("info", "pinned TLS certificate for %s", firstNonEmpty(entry.Name, addr))
	}

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

	// Size the read limit from the negotiated audio format. The announced
	// rate/channels/frameMS come from the peer, so clamp to the absolute
	// ceiling — a rogue peer must not be able to inflate this.
	frameBytes := int64(rate) * int64(frameMS) / 1000 * int64(channels) * 2
	limit := frameBytes * wsReadLimitFactor
	if limit <= 0 || limit > maxWSReadBytes {
		limit = maxWSReadBytes
	}
	ws.SetReadLimit(limit)

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
	d.broadcastStatusThrottled()
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

// pairWait returns how long the caller must wait before attempting to pair
// with addr, or an error if the target is temporarily locked out after too
// many rejections. It never blocks; the caller sleeps without holding mu.
func (d *Daemon) pairWait(addr string) (time.Duration, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.pairThrottle[addr]
	if st == nil {
		return 0, nil
	}
	if wait := time.Until(st.blockedUntil); wait > 0 {
		if st.fails >= pairMaxFails {
			return 0, fmt.Errorf("too many failed pairing attempts for %s; wait %s, then try again with a fresh code from the phone", addr, wait.Round(time.Second))
		}
		return wait, nil
	}
	return 0, nil
}

// pairNote records the outcome of a pairing attempt against addr. A success
// clears the throttle state; a rejection grows the backoff exponentially up
// to pairMaxBackoff, and after pairMaxFails consecutive rejections the
// target is locked out for pairLockout. A positive retryAfter (the phone's
// "retry_after" hint) extends the block accordingly.
func (d *Daemon) pairNote(addr string, ok bool, retryAfter time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ok {
		delete(d.pairThrottle, addr)
		return
	}
	if d.pairThrottle == nil {
		d.pairThrottle = make(map[string]*pairThrottleState)
	}
	st := d.pairThrottle[addr]
	if st == nil {
		st = &pairThrottleState{}
		d.pairThrottle[addr] = st
	}
	st.fails++
	if st.fails >= pairMaxFails {
		st.blockedUntil = time.Now().Add(pairLockout)
		return
	}
	if st.backoff <= 0 {
		st.backoff = pairBaseBackoff
	} else {
		st.backoff *= 2
		if st.backoff > pairMaxBackoff {
			st.backoff = pairMaxBackoff
		}
	}
	st.blockedUntil = time.Now().Add(st.backoff)
	if retryAfter > 0 && time.Now().Add(retryAfter).After(st.blockedUntil) {
		st.blockedUntil = time.Now().Add(retryAfter)
	}
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
	key := config.NormalizeAddress(addr)
	if wait, err := d.pairWait(key); err != nil {
		d.setState(StateError, err)
		d.Log("warn", "pairing throttled: %v", err)
		return entry, err
	} else if wait > 0 {
		d.Log("info", "pairing with %s throttled after failed attempts, waiting %s", addr, wait.Round(time.Second))
		select {
		case <-ctx.Done():
			d.setState(StateError, ctx.Err())
			return entry, ctx.Err()
		case <-time.After(wait):
		}
	}
	d.setState(StatePairing, nil)
	d.Log("info", "pairing with %s", addr)

	// Certificate pinning at pairing time (TOFU). Prefer the mDNS-advertised
	// "sha256" hint when the target was discovered; otherwise accept the
	// presented certificate and pin it below. The previously stored pin is
	// deliberately NOT enforced here: the 6-digit code is the recovery
	// authentication when the phone's certificate legitimately changed
	// (e.g. app reinstall).
	expectFP := d.mdnsFingerprintHint(ctx, addr)
	ws, observedFP, err := d.dialWSS(ctx, addr, expectFP)
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
			// Wrong code: count it toward brute-force throttling and honor
			// the phone's backoff hint when present.
			d.pairNote(key, false, time.Duration(reply.RetryAfter)*time.Second)
			err := fmt.Errorf("pairing rejected: %s", reply.Error)
			d.setState(StateError, err)
			d.Log("warn", "pairing failed: %v", err)
			return entry, err
		}
		if reply.Type != "paired" {
			continue
		}
		if err := checkAdvertisedFP(reply.CertSHA256, observedFP); err != nil {
			d.setState(StateError, err)
			d.Log("warn", "pairing failed: %v", err)
			return entry, err
		}
		entry.Token = reply.Token
		entry.Name = firstNonEmpty(reply.Name, entry.Name, addr)
		entry.ID = firstNonEmpty(reply.ID, entry.ID)
		entry.Address = addr
		entry.CertSHA256 = observedFP
		d.cfgMu.Lock()
		d.cfg.UpsertDevice(entry)
		saveErr := d.cfg.Save()
		d.cfgMu.Unlock()
		d.pairNote(key, true, 0)
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

// adbToken returns the pairing token to present on the ADB transport.
// The phone's ADB socket requires the same long-term pairing token as Wi-Fi,
// so the device must have been paired over Wi-Fi at least once. The ADB
// device serial cannot be matched to a saved entry, so exactly one paired
// device must exist; otherwise the choice would be ambiguous.
func (d *Daemon) adbToken() (string, error) {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	var token string
	count := 0
	for _, dev := range d.cfg.Devices {
		if dev.Token == "" {
			continue
		}
		count++
		token = dev.Token
	}
	switch {
	case count == 1:
		return token, nil
	case count == 0:
		return "", errors.New("no paired device: pair over Wi-Fi first (sawt pair HOST:PORT CODE)")
	default:
		return "", errors.New("multiple paired devices: unpair the ones you don't use (sawt unpair) or connect over Wi-Fi")
	}
}

func (d *Daemon) streamADB(ctx context.Context, serial string) error {
	if !adb.Available() {
		return errors.New("adb not found")
	}
	// Fail fast when there is no credential for the phone's ADB handshake.
	token, err := d.adbToken()
	if err != nil {
		return err
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
	// Only launch the app when it is not running. Starting it on every retry
	// restarts MainActivity every ~16 s (the ADB loop interval) and pulls the
	// user out of whatever they are doing. If the process exists but the
	// service is stopped (user pressed Stop), launching the activity would not
	// clear the app's userStopped flag anyway.
	if !m.AppRunning(ctx, "com.sawtplus.app") {
		if err := m.StartApp(ctx, "com.sawtplus.app"); err != nil {
			return err
		}
	} else {
		d.adbRunningHint.Do(func() {
			d.Log("info", "Sawt+ app is already running; not relaunching it")
		})
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

	// Protocol v2 handshake: present the pairing token first. The phone
	// closes the connection without starting capture if it is wrong.
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "TOKEN %s\n", token); err != nil {
		return fmt.Errorf("adb auth: %w", err)
	}

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
