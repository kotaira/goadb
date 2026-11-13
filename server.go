package adb

import (
	stderrors "errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kotaira/goadb/internal/errors"
	"github.com/kotaira/goadb/wire"
)

const (
	AdbExecutableName = "adb"

	// Default port the adb server listens on.
	AdbPort = 5037
)

type ServerConfig struct {
	// Path to the adb executable. If empty, the PATH environment variable will be searched.
	PathToAdb string

	// Host and port the adb server is listening on.
	// If not specified, will use the default port on localhost.
	Host string
	Port int
	// NoAutoStart leaves server lifecycle to the caller (for example, context-bounded operations).
	NoAutoStart bool

	// Dialer used to connect to the adb server.
	Dialer

	fs *filesystem
}

// Server knows how to start the adb server and connect to it.
type server interface {
	Start() error
	Dial() (*wire.Conn, error)
}

func roundTripSingleResponse(s server, req string) ([]byte, error) {
	conn, err := s.Dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return conn.RoundTripSingleResponse([]byte(req))
}

type realServer struct {
	config ServerConfig

	// Caches Host:Port so they don't have to be concatenated for every dial.
	address string
}

func newServer(config ServerConfig) (server, error) {
	if config.Dialer == nil {
		config.Dialer = tcpDialer{}
	}

	if config.Host == "" {
		config.Host = "localhost"
	}
	if config.Port == 0 {
		config.Port = AdbPort
	}

	if config.fs == nil {
		config.fs = localFilesystem
	}

	if config.PathToAdb == "" {
		path, err := config.fs.LookPath(AdbExecutableName)
		if err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "could not find %s in PATH", AdbExecutableName)
		}
		config.PathToAdb = path
	}
	if err := config.fs.IsExecutableFile(config.PathToAdb); err != nil {
		return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "invalid adb executable: %s", config.PathToAdb)
	}

	return &realServer{
		config:  config,
		address: fmt.Sprintf("%s:%d", config.Host, config.Port),
	}, nil
}

// Dial retries after starting the server unless NoAutoStart is set.
func (s *realServer) Dial() (*wire.Conn, error) {
	conn, err := s.config.Dial(s.address)
	if err != nil && !s.config.NoAutoStart {
		// Attempt to start the server and try again.
		if err = s.Start(); err != nil {
			return nil, errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting server for dial")
		}

		conn, err = s.config.Dial(s.address)
	}
	if err != nil {
		// Custom dialers may return raw network errors; client methods require *errors.Err.
		if _, ok := err.(*errors.Err); !ok {
			err = errors.WrapErrorf(err, errors.ServerNotAvailable, "error dialing %s", s.address)
		}
		return nil, err
	}
	return conn, nil
}

// StartServer ensures there is a server running.
func (s *realServer) Start() error {
	output, err := s.config.fs.CmdCombinedOutput(s.config.PathToAdb, "-L", fmt.Sprintf("tcp:%s", s.address), "start-server")
	outputStr := strings.TrimSpace(string(output))
	return errors.WrapErrorf(err, errors.ServerNotAvailable, "error starting server: %s\noutput:\n%s", err, outputStr)
}

// filesystem abstracts interactions with the local filesystem for testability.
type filesystem struct {
	// Wraps exec.LookPath.
	LookPath func(string) (string, error)

	// Returns nil if path is a regular file and executable by the current user.
	IsExecutableFile func(path string) error

	// Wraps exec.Command().CombinedOutput()
	CmdCombinedOutput func(name string, arg ...string) ([]byte, error)
}

var localFilesystem = &filesystem{
	LookPath: exec.LookPath,
	IsExecutableFile: func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return stderrors.New("not a regular file")
		}
		return isExecutable(path)
	},
	CmdCombinedOutput: func(name string, arg ...string) ([]byte, error) {
		return exec.Command(name, arg...).CombinedOutput()
	},
}
