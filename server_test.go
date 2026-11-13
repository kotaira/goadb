package adb

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/kotaira/goadb/v2/internal/errors"
	"github.com/kotaira/goadb/v2/wire"
	"github.com/stretchr/testify/assert"
)

type failingDialer struct{ err error }

func (d failingDialer) Dial(string) (*wire.Conn, error) { return nil, d.err }

func TestNoAutoStartReturnsDialError(t *testing.T) {
	srv := &realServer{config: ServerConfig{
		NoAutoStart: true,
		Dialer:      failingDialer{context.Canceled},
		fs: &filesystem{CmdCombinedOutput: func(string, ...string) ([]byte, error) {
			t.Fatal("cancelled operation must not start the ADB server")
			return nil, nil
		}},
	}}
	_, err := srv.Dial()
	if !HasErrCode(err, ServerNotAvailable) {
		t.Fatalf("expected wrapped dial error, got %v", err)
	}
}

func TestForwardKillTransportFailure(t *testing.T) {
	srv := &MockServer{
		Status:   wire.StatusSuccess,
		Messages: []string{"serial"},
		Errs:     []error{nil, nil, nil, nil, nil, nil, errors.Errorf(errors.DeviceNotFound, "device offline")},
	}
	dev := (&Adb{server: srv}).Device(DeviceWithSerial("serial"))
	if err := dev.ForwardKill("tcp:38091"); !HasErrCode(err, DeviceNotFound) {
		t.Fatalf("expected device offline, got %v (trace %v)", err, srv.Trace)
	}
}

func TestForwardListTransportFailure(t *testing.T) {
	srv := &MockServer{
		Status:   wire.StatusSuccess,
		Messages: []string{"serial"},
		Errs:     []error{nil, nil, nil, nil, nil, nil, errors.Errorf(errors.DeviceNotFound, "device offline")},
	}
	dev := (&Adb{server: srv}).Device(DeviceWithSerial("serial"))
	if _, err := dev.ForwardList(); !HasErrCode(err, DeviceNotFound) {
		t.Fatalf("expected device offline, got %v (trace %v)", err, srv.Trace)
	}
}

func TestCustomDialerErrorsReachDeviceCallers(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code ErrCode
	}{
		{"dns_timeout", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "i/o timeout", Name: "localhost", IsTimeout: true}}, ServerNotAvailable},
		{"cancelled", context.Canceled, ServerNotAvailable},
		{"deadline", context.DeadlineExceeded, ServerNotAvailable},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, ServerNotAvailable},
		{"typed", errors.Errorf(errors.NetworkError, "existing error"), NetworkError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &realServer{
				address: "127.0.0.1:5037",
				config: ServerConfig{
					Dialer: failingDialer{tc.err},
					fs: &filesystem{CmdCombinedOutput: func(string, ...string) ([]byte, error) {
						return nil, nil
					}},
				},
			}
			dev := (&Adb{server: srv}).Device(DeviceWithSerial("172.16.250.248:5555"))
			calls := map[string]func() error{
				"cleanup_command": func() error {
					_, err := dev.RunCommand("am", "force-stop", "io.appium.uiautomator2.server")
					return err
				},
				"state_probe":       func() error { _, err := dev.State(); return err },
				"root":              func() error { return dev.Root() },
				"unroot":            func() error { return dev.UnRoot() },
				"shell":             func() error { _, err := dev.RunCommandWithShell("logcat"); return err },
				"interactive_shell": func() error { _, err := dev.OpenShell(); return err },
				"forward":           func() error { return dev.Forward("tcp:38091", "tcp:6790", false) },
				"forward_list":      func() error { _, err := dev.ForwardList(); return err },
				"push":              func() error { return dev.Push([]byte("data"), "/data/local/tmp/test") },
				"pull":              func() error { _, err := dev.Pull("/data/local/tmp/test"); return err },
				"local_socket":      func() error { _, err := dev.DialLocalAbstractSocket("scrcpy"); return err },
				"device_info":       func() error { _, err := dev.DeviceInfo(); return err },
				"forward_cleanup":   func() error { return dev.ForwardKill("tcp:38091") },
			}
			for name, call := range calls {
				t.Run(name, func(t *testing.T) {
					err := call() // A panic fails the test, as it would terminate the serials process.
					if !HasErrCode(err, tc.code) {
						t.Fatalf("expected code %v, got %v", tc.code, err)
					}
					cause := err
					for cause != tc.err {
						wrapped, ok := cause.(*errors.Err)
						if !ok || wrapped.Cause == nil {
							t.Fatalf("original dial error lost: %v", err)
						}
						cause = wrapped.Cause
					}
				})
			}
		})
	}
}

func TestNewServer_ZeroConfig(t *testing.T) {
	config := ServerConfig{fs: &filesystem{
		LookPath: func(name string) (string, error) {
			if name == AdbExecutableName {
				return "/bin/adb", nil
			}
			return "", fmt.Errorf("invalid name: %s", name)
		},
		IsExecutableFile: func(path string) error {
			if path == "/bin/adb" {
				return nil
			}
			return fmt.Errorf("wrong path: %s", path)
		},
	}}

	serverIf, err := newServer(config)
	server := serverIf.(*realServer)
	assert.NoError(t, err)
	assert.IsType(t, tcpDialer{}, server.config.Dialer)
	assert.Equal(t, "localhost", server.config.Host)
	assert.Equal(t, AdbPort, server.config.Port)
	assert.Equal(t, fmt.Sprintf("localhost:%d", AdbPort), server.address)
	assert.Equal(t, "/bin/adb", server.config.PathToAdb)
}

type MockDialer struct{}

func (d MockDialer) Dial(address string) (*wire.Conn, error) {
	return nil, nil
}

func TestNewServer_CustomConfig(t *testing.T) {
	config := ServerConfig{
		Dialer:    MockDialer{},
		Host:      "foobar",
		Port:      1,
		PathToAdb: "/bin/adb",
		fs: &filesystem{
			IsExecutableFile: func(path string) error {
				if path == "/bin/adb" {
					return nil
				}
				return fmt.Errorf("wrong path: %s", path)
			},
		},
	}

	serverIf, err := newServer(config)
	server := serverIf.(*realServer)
	assert.NoError(t, err)
	assert.IsType(t, MockDialer{}, server.config.Dialer)
	assert.Equal(t, "foobar", server.config.Host)
	assert.Equal(t, 1, server.config.Port)
	assert.Equal(t, fmt.Sprintf("foobar:1"), server.address)
	assert.Equal(t, "/bin/adb", server.config.PathToAdb)
}

func TestNewServer_AdbNotFound(t *testing.T) {
	config := ServerConfig{fs: &filesystem{
		LookPath: func(name string) (string, error) {
			return "", fmt.Errorf("executable not found: %s", name)
		},
	}}

	_, err := newServer(config)
	assert.EqualError(t, err, "ServerNotAvailable: could not find adb in PATH")
}
