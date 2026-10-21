package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"

	"github.com/apot4711-web/sawt-plus/internal/adb"
	"github.com/apot4711-web/sawt-plus/internal/api"
	"github.com/apot4711-web/sawt-plus/internal/client"
	"github.com/apot4711-web/sawt-plus/internal/config"
	"github.com/apot4711-web/sawt-plus/internal/daemon"
	"github.com/apot4711-web/sawt-plus/internal/version"
)

const androidPkg = "com.sawtplus.app"

func main() {
	log.SetOutput(os.Stderr)
	log.SetTimeFormat("15:04:05")
	log.SetReportTimestamp(true)
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printHelp()
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "daemon", "serve":
		return runDaemon(rest)
	case "shutdown", "quit":
		return runShutdown(rest)
	case "web":
		return runWeb(rest)
	case "discover", "scan", "ls":
		return runDiscover(rest)
	case "pair":
		return runPair(rest)
	case "unpair", "forget":
		return runUnpair(rest)
	case "connect", "run":
		return runConnect(rest)
	case "disconnect", "stop":
		return runDisconnect(rest)
	case "status":
		return runStatus(rest)
	case "logs", "log":
		return runLogs(rest)
	case "gain":
		return runGain(rest)
	case "mute":
		return runMute(rest)
	case "adb":
		return runADB(rest)
	case "build":
		return runBuild(rest)
	case "install":
		return runInstall(rest)
	case "version", "--version", "-v":
		fmt.Printf("%s %s\n", version.App, version.Version)
		return 0
	case "help", "--help", "-h":
		printHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printHelp()
		return 2
	}
}

// ---------------------------------------------------------------------------
// Daemon lifecycle
// ---------------------------------------------------------------------------

func newDaemon() (*daemon.Daemon, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	return daemon.New(cfg), cfg, nil
}

func runDaemon(args []string) int {
	lock, err := config.AcquireDaemonLock()
	if err != nil {
		if errors.Is(err, config.ErrDaemonRunning) {
			log.Error("daemon", "err", "another sawt daemon is already running")
		} else {
			log.Error("lock", "err", err)
		}
		return 1
	}
	defer lock.Close()

	d, cfg, err := newDaemon()
	if err != nil {
		log.Error("load config", "err", err)
		return 1
	}
	token, err := config.LoadAPIToken()
	if err != nil {
		log.Error("load API token", "err", err)
		return 1
	}
	ctx, cancel := signalContext()
	defer cancel()

	server := api.New(d, log.Default(), token)
	server.SetShutdown(cancel)
	go func() {
		if err := server.Start(ctx, cfg.Daemon.WebAddr); err != nil {
			log.Error("http interface", "err", err)
			cancel()
		}
	}()
	d.Log("info", "sawt %s started", version.Version)
	log.Info("daemon running", "web", "http://"+cfg.Daemon.WebAddr, "log", daemonLogPath())
	d.StartAutoConnect()

	<-ctx.Done()
	d.Shutdown()
	return 0
}

// runShutdown stops a running daemon without starting a new one.
func runShutdown(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		log.Error("load config", "err", err)
		return 1
	}
	token, err := config.LoadAPIToken()
	if err != nil {
		log.Error("load API token", "err", err)
		return 1
	}
	c := client.New(cfg.Daemon.WebAddr, token)
	if !daemonAlive(c) {
		fmt.Println("daemon is not running")
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Shutdown(ctx); err != nil {
		log.Error("shutdown", "err", err)
		return 1
	}
	fmt.Println("daemon stopped")
	return 0
}

// ensureDaemon returns a client for the running daemon and starts a headless
// daemon in the background when needed (hybrid mode).
func ensureDaemon() (*client.Client, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	token, err := config.LoadAPIToken()
	if err != nil {
		return nil, nil, err
	}
	c := client.New(cfg.Daemon.WebAddr, token)
	if daemonAlive(c) {
		return c, cfg, nil
	}
	if os.Getenv("SAWT_NO_AUTOSTART") == "1" {
		return nil, cfg, errors.New("daemon is not running; start it with: sawt daemon")
	}
	if err := startDaemonBackground(); err != nil {
		return nil, cfg, fmt.Errorf("start daemon: %w", err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if daemonAlive(c) {
			return c, cfg, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, cfg, fmt.Errorf("daemon did not start; see %s", daemonLogPath())
}

func daemonAlive(c *client.Client) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	return c.Alive(ctx)
}

func startDaemonBackground() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(daemonLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return err
	}
	_ = cmd.Process.Release()
	return f.Close()
}

func daemonLogPath() string {
	return filepath.Join(config.Dir(), "daemon.log")
}

// ---------------------------------------------------------------------------
// Web and discovery
// ---------------------------------------------------------------------------

func runWeb(args []string) int {
	open := !hasFlag(args, "--no-open", "-n")
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	target := "http://" + c.Addr() + "/?token=" + url.QueryEscape(c.Token())
	if open {
		openBrowser(target)
	} else {
		fmt.Println(target)
	}
	return 0
}

func runDiscover(args []string) int {
	d, _, err := newDaemon()
	if err != nil {
		log.Error("load config", "err", err)
		return 1
	}
	defer d.Shutdown()
	timeout := 3 * time.Second
	if v := flagValue(args, "--timeout", "-t"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			timeout = parsed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+2*time.Second)
	defer cancel()
	devices, err := d.Discover(ctx, timeout)
	if err != nil {
		log.Error("discover", "err", err)
		return 1
	}
	if len(devices) == 0 {
		fmt.Println("no devices found")
		return 0
	}
	fmt.Printf("%-24s %-24s %-8s %-12s %s\n", "NAME", "ADDRESS", "TRANSPORT", "VERSION", "PAIRING")
	for _, dev := range devices {
		pairing := ""
		if dev.Pairing {
			pairing = "required"
		}
		transport := dev.Transport
		if transport == "" {
			transport = "wifi"
		}
		fmt.Printf("%-24s %-24s %-8s %-12s %s\n", truncate(dev.Label(), 24), dev.Address, transport, dev.Version, pairing)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Pairing and connection
// ---------------------------------------------------------------------------

func runPair(args []string) int {
	noConnect := hasFlag(args, "--no-connect")
	var positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		positional = append(positional, a)
	}

	var target, code string
	switch {
	case len(positional) == 1 && strings.HasPrefix(positional[0], "sawtplus://"):
		var err error
		target, code, err = parsePairURI(positional[0])
		if err != nil {
			log.Error("pair", "err", err)
			return 2
		}
	case len(positional) >= 2:
		target, code = positional[0], positional[1]
	default:
		fmt.Fprintln(os.Stderr, "usage: sawt pair HOST:PORT CODE")
		fmt.Fprintln(os.Stderr, "   or: sawt pair 'sawtplus://pair?host=...&port=...&code=...'")
		return 2
	}

	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	entry, err := c.Pair(ctx, target, code)
	if err != nil {
		log.Error("pair", "err", err)
		return 1
	}
	fmt.Printf("paired with %s (%s)\n", firstNonEmpty(entry.Name, entry.Address), entry.Address)
	if noConnect {
		return 0
	}

	if err := c.Connect(ctx, entry.Address); err != nil {
		log.Warn("paired, but could not start streaming", "err", err)
		return 0
	}
	st, err := waitStreaming(ctx, c, 15*time.Second)
	if err != nil {
		log.Warn("paired, but streaming did not start", "err", err)
		printStatus(st, false)
		return 0
	}
	fmt.Println("streaming started")
	printStatus(st, false)
	return 0
}

func parsePairURI(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "sawtplus" || u.Host != "pair" {
		return "", "", fmt.Errorf("unsupported pairing URI %q", raw)
	}
	q := u.Query()
	host := q.Get("host")
	port := q.Get("port")
	code := q.Get("code")
	if host == "" || code == "" {
		return "", "", errors.New("pairing URI is missing host or code")
	}
	if port == "" {
		port = "8765"
	}
	return net.JoinHostPort(host, port), code, nil
}

func runConnect(args []string) int {
	target := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		target = a
		break
	}
	timeout := 15 * time.Second
	if v := flagValue(args, "--timeout", "-t"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			timeout = parsed
		}
	}

	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+15*time.Second)
	defer cancel()
	if err := c.Connect(ctx, target); err != nil {
		log.Error("connect", "err", err)
		return 1
	}
	st, err := waitStreaming(ctx, c, timeout)
	if err != nil {
		if st.State == daemon.StatePairing {
			fmt.Fprintf(os.Stderr, "pairing required; run: sawt pair %s CODE\n", firstNonEmpty(st.Address, target))
			return 1
		}
		log.Error("connect", "err", err)
		printStatus(st, false)
		return 1
	}
	fmt.Println("streaming started")
	printStatus(st, false)
	return 0
}

func waitStreaming(ctx context.Context, c *client.Client, timeout time.Duration) (daemon.Status, error) {
	deadline := time.Now().Add(timeout)
	var last daemon.Status
	for {
		st, err := c.Status(ctx)
		if err == nil {
			last = st
			switch st.State {
			case daemon.StateStreaming:
				return st, nil
			case daemon.StatePairing:
				return st, errors.New("pairing required")
			}
		}
		if time.Now().After(deadline) {
			msg := fmt.Sprintf("timed out waiting for stream (state=%s)", firstNonEmpty(string(last.State), "unknown"))
			if last.Error != "" {
				msg += ": " + last.Error
			}
			return last, errors.New(msg)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func runDisconnect(args []string) int {
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Disconnect(ctx); err != nil {
		log.Error("disconnect", "err", err)
		return 1
	}
	fmt.Println("disconnected")
	return 0
}

func runUnpair(args []string) int {
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var target string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}
	if target == "" {
		st, err := c.Status(ctx)
		if err != nil {
			log.Error("status", "err", err)
			return 1
		}
		target = st.Address
	}
	if strings.TrimSpace(target) == "" {
		fmt.Fprintln(os.Stderr, "usage: sawt unpair [TARGET]")
		return 2
	}
	if err := c.Unpair(ctx, target); err != nil {
		log.Error("unpair", "err", err)
		return 1
	}
	fmt.Printf("unpaired %s\n", target)
	return 0
}

// ---------------------------------------------------------------------------
// Status and controls
// ---------------------------------------------------------------------------

func runStatus(args []string) int {
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := c.Status(ctx)
	if err != nil {
		log.Error("status", "err", err)
		return 1
	}
	printStatus(st, hasFlag(args, "--json", "-j"))
	return 0
}

func printStatus(st daemon.Status, asJSON bool) {
	if asJSON {
		data, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(data))
		return
	}
	device := firstNonEmpty(st.Device, "-")
	mute := "off"
	if st.Mute {
		mute = "on"
	}
	fmt.Printf("%-11s %s\n", "state:", st.State)
	fmt.Printf("%-11s %s\n", "device:", device)
	fmt.Printf("%-11s %s\n", "address:", firstNonEmpty(st.Address, "-"))
	fmt.Printf("%-11s %s\n", "transport:", firstNonEmpty(st.Transport, "-"))
	fmt.Printf("%-11s %s\n", "source:", firstNonEmpty(st.Source, "-"))
	fmt.Printf("%-11s %d Hz, %d ch, %d ms\n", "audio:", st.Rate, st.Channels, st.FrameMS)
	fmt.Printf("%-11s %d connected, %d frames, %d dropped, rtt %d ms\n", "traffic:", st.Clients, st.Frames, st.Dropped, st.RTTMS)
	fmt.Printf("%-11s gain %.2fx, mute %s\n", "input:", st.Gain, mute)
	if st.Error != "" {
		fmt.Printf("%-11s %s\n", "error:", st.Error)
	}
}

func runLogs(args []string) int {
	limit := 20
	if v := flagValue(args, "-n", "--lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logs, err := c.Logs(ctx)
	if err != nil {
		log.Error("logs", "err", err)
		return 1
	}
	if hasFlag(args, "--json", "-j") {
		data, _ := json.MarshalIndent(logs, "", "  ")
		fmt.Println(string(data))
		return 0
	}
	if len(logs) > limit {
		logs = logs[len(logs)-limit:]
	}
	for _, entry := range logs {
		fmt.Printf("%s %-5s %s\n", entry.Time.Format("15:04:05"), strings.ToUpper(entry.Level), entry.Message)
	}
	return 0
}

func runGain(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: sawt gain 1.5x")
		return 2
	}
	gain, err := config.ParseGain(args[0])
	if err != nil {
		log.Error("gain", "err", err)
		return 2
	}
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Gain(ctx, gain); err != nil {
		log.Error("gain", "err", err)
		return 1
	}
	fmt.Printf("gain set to %.2fx\n", gain)
	return 0
}

func runMute(args []string) int {
	c, _, err := ensureDaemon()
	if err != nil {
		log.Error("daemon", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mute := false
	switch strings.ToLower(firstNonEmpty(args...)) {
	case "", "toggle":
		st, err := c.Status(ctx)
		if err != nil {
			log.Error("status", "err", err)
			return 1
		}
		mute = !st.Mute
	case "on", "true", "1", "mute":
		mute = true
	case "off", "false", "0", "unmute":
		mute = false
	default:
		fmt.Fprintln(os.Stderr, "usage: sawt mute [on|off|toggle]")
		return 2
	}
	if err := c.Mute(ctx, mute); err != nil {
		log.Error("mute", "err", err)
		return 1
	}
	if mute {
		fmt.Println("muted")
	} else {
		fmt.Println("unmuted")
	}
	return 0
}

// ---------------------------------------------------------------------------
// ADB, build, install
// ---------------------------------------------------------------------------

func runADB(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sawt adb [devices|pair|connect|disconnect|run|install]")
		return 2
	}
	if !adb.Available() {
		log.Error("adb not found; install android-tools")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	switch args[0] {
	case "devices", "ls":
		m := adb.New(serialFlag(args[1:]))
		devices, err := m.Devices(ctx)
		if err != nil {
			log.Error("adb devices", "err", err)
			return 1
		}
		for _, d := range devices {
			fmt.Printf("%-24s %-12s %s\n", d.Serial, d.State, d.Model)
		}
		return 0
	case "pair":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: sawt adb pair HOST:PORT CODE")
			return 2
		}
		m := adb.New("")
		if err := m.Pair(ctx, args[1], args[2]); err != nil {
			log.Error("adb pair", "err", err)
			return 1
		}
		fmt.Println("paired successfully")
		return 0
	case "connect":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: sawt adb connect HOST:PORT")
			return 2
		}
		m := adb.New("")
		if err := m.Connect(ctx, args[1]); err != nil {
			log.Error("adb connect", "err", err)
			return 1
		}
		fmt.Println("connected")
		return 0
	case "disconnect":
		m := adb.New("")
		addr := ""
		if len(args) > 1 {
			addr = args[1]
		}
		if err := m.Disconnect(ctx, addr); err != nil {
			log.Error("adb disconnect", "err", err)
			return 1
		}
		return 0
	case "run":
		serial := serialFlag(args[1:])
		c, _, err := ensureDaemon()
		if err != nil {
			log.Error("daemon", "err", err)
			return 1
		}
		if err := c.Connect(ctx, "adb:"+serial); err != nil {
			log.Error("adb run", "err", err)
			return 1
		}
		st, err := waitStreaming(ctx, c, 60*time.Second)
		if err != nil {
			log.Error("adb run", "err", err)
			printStatus(st, false)
			return 1
		}
		fmt.Println("streaming started over ADB")
		printStatus(st, false)
		return 0
	case "install":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: sawt adb install path/to/app.apk")
			return 2
		}
		if err := adb.Install(ctx, args[1], serialFlag(args[2:])); err != nil {
			log.Error("adb install", "err", err)
			return 1
		}
		fmt.Println("installed")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown adb command %q\n", args[0])
		return 2
	}
}

func runBuild(args []string) int {
	task := ":app:assembleDebug"
	if os.Getenv("SAWT_PROFILE") == "release" {
		task = ":app:assembleRelease"
	}
	return runGradle(task)
}

func runInstall(args []string) int {
	apk := "app/build/outputs/apk/debug/app-debug.apk"
	if os.Getenv("SAWT_PROFILE") == "release" {
		apk = "app/build/outputs/apk/release/app-release.apk"
	}
	if len(args) > 0 {
		apk = args[0]
	}
	abs, err := filepath.Abs(apk)
	if err != nil {
		log.Error("resolve apk", "err", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := adb.Install(ctx, abs, ""); err != nil {
		log.Error("install", "err", err)
		return 1
	}
	return 0
}

func runGradle(task string) int {
	gradlew := "./gradlew"
	if _, err := os.Stat(gradlew); err != nil {
		log.Error("gradlew not found; run from the project root")
		return 1
	}
	cmd := exec.Command(gradlew, task)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		log.Error("gradle", "task", task, "err", err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func printHelp() {
	fmt.Print(`Sawt+ - use an Android device as a wireless microphone

Usage:
  sawt [command]

Commands:
  daemon              Run the headless daemon and local web API in this terminal
  shutdown            Stop a daemon running in the background
  web [--no-open]     Open the browser interface (starts the daemon if needed)
  status [--json]     Show stream, device, gain, and mute state
  logs [-n N]         Show recent daemon log lines
  discover            Scan for Wi-Fi and USB/ADB devices
  pair TARGET CODE    Pair by host:port and code (also accepts a sawtplus:// URI)
  pair --no-connect   Pair only, do not start streaming
  unpair [TARGET]     Forget a saved pairing (default: current device)
  connect [TARGET]    Connect and stream (target: host:port, name, or adb:SERIAL)
  disconnect, stop    Stop streaming
  gain 1.5x           Set input level (0..8, or 0%..800%)
  mute [on|off]       Mute or unmute the microphone (default: toggle)
  adb <cmd>           ADB fallback: devices, pair, connect, disconnect, run, install
  version             Print the version
  help                Show this help

Most commands start the headless daemon automatically if it is not running.
The daemon log is at ~/.config/sawt-plus/daemon.log.

Environment:
  SAWT_NO_AUTOSTART         set to 1 to require an explicit daemon
  ANDROID_SERIAL            ADB serial for the fallback path
`)
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, name := range names {
			if a == name {
				return true
			}
		}
	}
	return false
}

func flagValue(args []string, names ...string) string {
	for i, a := range args {
		for _, name := range names {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"=")
			}
		}
	}
	return ""
}

func serialFlag(args []string) string {
	for i, a := range args {
		if (a == "-s" || a == "--serial") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return os.Getenv("ANDROID_SERIAL")
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

func openBrowser(url string) {
	for _, bin := range []string{"xdg-open", "sensible-browser", "x-www-browser"} {
		if _, err := exec.LookPath(bin); err == nil {
			_ = exec.Command(bin, url).Start()
			return
		}
	}
	log.Info("open this URL in your browser", "url", url)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
