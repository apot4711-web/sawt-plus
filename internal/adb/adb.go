// Package adb wraps the Android Debug Bridge for the USB and wireless ADB
// fallback paths.
package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Device struct {
	Serial    string
	State     string
	Model     string
	Product   string
	Device    string
	Transport string
}

type Manager struct {
	Serial string
}

func Available() bool {
	_, err := exec.LookPath("adb")
	return err == nil
}

func New(serial string) *Manager { return &Manager{Serial: serial} }

func (m *Manager) args(extra ...string) []string {
	if m.Serial != "" {
		return append([]string{"-s", m.Serial}, extra...)
	}
	return extra
}

func (m *Manager) run(ctx context.Context, extra ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "adb", m.args(extra...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("adb %s: %s", strings.Join(extra, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (m *Manager) runLong(ctx context.Context, timeout time.Duration, extra ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "adb", m.args(extra...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("adb %s: %s", strings.Join(extra, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (m *Manager) Devices(ctx context.Context) ([]Device, error) {
	out, err := m.runLong(ctx, 10*time.Second, "devices", "-l")
	if err != nil {
		return nil, err
	}
	var devices []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := Device{Serial: fields[0], State: fields[1]}
		for _, f := range fields[2:] {
			key, value, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			switch key {
			case "model":
				d.Model = value
			case "product":
				d.Product = value
			case "device":
				d.Device = value
			case "transport_id":
				d.Transport = value
			}
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (m *Manager) Wait(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err := m.runLong(ctx, 2*time.Minute, "wait-for-device")
	return err
}

func (m *Manager) Grant(ctx context.Context, pkg string) error {
	perms := []string{"android.permission.RECORD_AUDIO", "android.permission.POST_NOTIFICATIONS"}
	var missing []string
	for _, p := range perms {
		out, err := m.run(ctx, "shell", "dumpsys", "package", pkg)
		if err == nil && strings.Contains(out, p+": granted=true") {
			continue
		}
		if _, err := m.run(ctx, "shell", "pm", "grant", pkg, p); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("could not grant permissions: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (m *Manager) StartApp(ctx context.Context, pkg string) error {
	_, err := m.run(ctx, "shell", "am", "start", "-n", pkg+"/.MainActivity")
	return err
}

// Forward opens a local TCP port that forwards to the app's abstract socket.
func (m *Manager) Forward(ctx context.Context, remote string) (int, error) {
	if remote == "" {
		remote = "localabstract:sawt-plus"
	}
	out, err := m.run(ctx, "forward", "tcp:0", remote)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("unexpected adb forward output %q", out)
	}
	return port, nil
}

func (m *Manager) RemoveForward(ctx context.Context, port int) {
	_, _ = m.run(ctx, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
}

func (m *Manager) Connect(ctx context.Context, address string) error {
	out, err := m.runLong(ctx, 30*time.Second, "connect", address)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(out), "failed") || strings.Contains(strings.ToLower(out), "unable") {
		return errors.New(out)
	}
	return nil
}

func (m *Manager) Disconnect(ctx context.Context, address string) error {
	_, err := m.runLong(ctx, 15*time.Second, "disconnect", address)
	return err
}

func (m *Manager) Pair(ctx context.Context, address, code string) error {
	out, err := m.runLong(ctx, 60*time.Second, "pair", address, code)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(out), "successfully paired") {
		return errors.New(out)
	}
	return nil
}

var mdnsLine = regexp.MustCompile(`^\s*(\S+)\s+(\S+)`)

func (m *Manager) MDNSServices(ctx context.Context) ([]string, error) {
	out, err := m.runLong(ctx, 10*time.Second, "mdns", "services")
	if err != nil {
		return nil, err
	}
	var services []string
	for _, line := range strings.Split(out, "\n") {
		match := mdnsLine.FindStringSubmatch(line)
		if len(match) == 3 {
			services = append(services, match[1]+" "+match[2])
		}
	}
	return services, nil
}

func Install(ctx context.Context, apk string, serial string) error {
	if _, err := exec.LookPath("adb"); err != nil {
		return errors.New("adb not found")
	}
	args := []string{"install", "-r", "-t", "-g", apk}
	if serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "adb", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb install: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
