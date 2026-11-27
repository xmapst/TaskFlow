package config

import (
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/pflag"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/pkg/utility"
)

// AppConfig holds TaskFlow's startup settings.
type AppConfig struct {
	RootDir        string
	LogOutput      string
	LogLevel       string
	SelfURL        string
	Addr           string
	AllowIPs       []string
	TrustedProxies []string
	ExecTimeout    time.Duration
	PoolSize       int
}

// App is populated by CLI flag parsing before the service starts.
// Treat it as read-only once the service is running.
var App = defaultAppConfig()

func defaultAppConfig() AppConfig {
	return AppConfig{
		RootDir:     utility.DefaultDir(api.ServiceName),
		LogOutput:   "file",
		LogLevel:    "debug",
		Addr:        "tcp://127.0.0.1:2376",
		ExecTimeout: 24 * time.Hour,
		PoolSize:    runtime.NumCPU() * 2,
	}
}

// BindFlags registers startup options on the command's persistent and local flag sets.
func (c *AppConfig) BindFlags(persistent, local *pflag.FlagSet) {
	persistent.StringVar(&c.RootDir, "root_dir", c.RootDir, "root directory")
	persistent.StringVar(&c.LogOutput, "log_output", c.LogOutput, "log output [file,stdout]")
	persistent.StringVar(&c.LogLevel, "log_level", c.LogLevel, "log level [debug,info,warn,error]")
	persistent.StringVar(&c.SelfURL, "self_url", c.SelfURL, "self-update URL")
	local.StringVar(&c.Addr, "addr", c.Addr, "listening address")
	local.StringSliceVar(&c.AllowIPs, "allow_ips", c.AllowIPs, "client IP allowlist: comma-separated IPs or CIDRs (e.g. 10.0.0.1,192.168.0.0/16); empty allows all. Unix socket and named pipe clients are not filtered.")
	local.StringSliceVar(&c.TrustedProxies, "trusted_proxies", c.TrustedProxies, "reverse proxy / load balancer IPs/CIDRs that are trusted to report the real client address: their PROXY protocol header and their X-Forwarded-For, X-Real-IP, CF-Connecting-IP, True-Client-IP and X-Client-IP headers are honoured (used by allow_ips and the access log); the same information from any other peer is ignored.")
	local.DurationVar(&c.ExecTimeout, "exec_timeout", c.ExecTimeout, "maximum task execution time")
	local.IntVar(&c.PoolSize, "pool_size", c.PoolSize, "initial worker pool size")
}

func (c AppConfig) LogDir() string       { return filepath.Join(c.RootDir, "logs") }
func (c AppConfig) TempDir() string      { return filepath.Join(c.RootDir, "temp") }
func (c AppConfig) DataDir() string      { return filepath.Join(c.RootDir, "data") }
func (c AppConfig) ScriptDir() string    { return filepath.Join(c.RootDir, "scripts") }
func (c AppConfig) ReplayDir() string    { return filepath.Join(c.RootDir, "replays") }
func (c AppConfig) WorkspaceDir() string { return filepath.Join(c.RootDir, "workspace") }
