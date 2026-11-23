package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The DynamoDB shard limits. They duplicate store.DefaultJobShards, store.DefaultDependentShards
// and store.MaxShards because importing the store here would pull every database driver into
// config; a test in package store keeps the two in step.
const (
	DefaultDynamoJobShards       = 8
	DefaultDynamoDependentShards = 1
	MaxDynamoShards              = 64
)

// Server is the service-wide configuration, read from the environment. Repo files
// can tighten the limits here, never widen them. Every URL comes from here and
// never from a repo file, so a repo cannot point ripple at an internal host.
type Server struct {
	Listen        string
	WebhookSecret string
	GitHubAppID   string
	GitHubKeyFile string
	GitHubAPIURL  string
	GitHubGitURL  string
	FeedURL       string
	// GoReleasesURL serves the Go release list (go-toolchain updates).
	GoReleasesURL string
	FeedInterval  time.Duration
	// OSV: advisories as an event source; off unless OSV is true.
	OSV         bool
	OSVURL      string
	OSVDumpURL  string
	OSVInterval time.Duration
	OSVMaxFetch int
	GoProxy     string
	// GoProxyUser and GoProxyToken are the credential for GoProxy (HTTP basic auth). They
	// reach only the go commands that run no repository code, and need RIPPLE_GOPROXY set to a private proxy. GoCC is the C compiler cgo
	// verification uses; empty is cc.
	GoProxyUser, GoProxyToken, GoCC string
	Sandbox                         string // none, auto, bwrap, seatbelt, container, docker, podman
	// Container* configure the container, docker and podman sandboxes (docs/sandbox.md).
	ContainerImage, ContainerCPUs, ContainerMemory, ContainerPull, ContainerNetwork string
	Store                                                                           string // memory, postgres, dynamodb
	PostgresDSN                                                                     string
	DynamoTable                                                                     string
	DynamoEndpoint                                                                  string
	// DynamoJobShards and DynamoDependentShards spread the pending-job queue and each
	// package's dependents over that many index partitions (1..MaxDynamoShards).
	DynamoJobShards       int
	DynamoDependentShards int
	MinimumReleaseAge     time.Duration
	OpenPRLimit           int
	AllowAutomerge        bool
	Workers               int
	WorkDir               string
	// MaxFiles and MaxDependencies cap one repo scan; a repo over either is skipped.
	MaxFiles        int
	MaxDependencies int
	// Event queue: workers overall and per installation, and the persisted backlog
	// overall and per installation (past either cap the receiver answers 503).
	EventWorkers                int
	EventWorkersPerInstallation int
	EventBacklog                int
	EventInstallationBacklog    int
	EventDeadRetention          time.Duration
}

// Default scan caps (RIPPLE_MAX_FILES, RIPPLE_MAX_DEPENDENCIES).
const (
	DefaultMaxFiles        = 100_000
	DefaultMaxDependencies = 5_000
)

// ServerEnv documents every variable; `ripple config validate --server` prints it.
const ServerEnv = `RIPPLE_LISTEN                 listen address (default :8080)
RIPPLE_WEBHOOK_SECRET         GitHub App webhook secret (required)
RIPPLE_GITHUB_APP_ID          GitHub App id (required)
RIPPLE_GITHUB_KEY_FILE        path to the App private key PEM (required)
RIPPLE_GITHUB_API_URL         default https://api.github.com
RIPPLE_GITHUB_GIT_URL         default https://github.com
RIPPLE_FEED_URL               default https://index.golang.org
RIPPLE_FEED_INTERVAL          default 1m
RIPPLE_GO_RELEASES_URL        Go release list for go-toolchain updates (default https://go.dev/dl/)
RIPPLE_OSV                    true turns on OSV security advisories as an event source (default false)
RIPPLE_OSV_URL                OSV API, default https://api.osv.dev
RIPPLE_OSV_DUMP_URL           OSV bucket, default https://storage.googleapis.com/osv-vulnerabilities
RIPPLE_OSV_INTERVAL           default 10m
RIPPLE_OSV_MAX_FETCH          advisory records fetched per poll (default 100)
RIPPLE_GOPROXY                module proxy used to verify major bumps (default https://proxy.golang.org)
RIPPLE_GOPROXY_USER           user for RIPPLE_GOPROXY (HTTP basic auth); with RIPPLE_GOPROXY_TOKEN, sent only to repos listing private-modules
RIPPLE_GOPROXY_TOKEN          password or token for RIPPLE_GOPROXY; needs an https proxy
RIPPLE_GO_CC                  C compiler for repos with cgo: true (default: go's own, gcc or clang)
RIPPLE_STORE                  memory (default), postgres or dynamodb
RIPPLE_POSTGRES_DSN           required for postgres
RIPPLE_DYNAMODB_TABLE         default ripple (dynamodb)
RIPPLE_DYNAMODB_ENDPOINT      optional, for DynamoDB Local
RIPPLE_DYNAMODB_JOB_SHARDS    default 8 (1-64): partitions holding pending jobs; a due-jobs poll costs this many queries
RIPPLE_DYNAMODB_DEPENDENT_SHARDS  default 1 (1-64): partitions per package's dependents; each lookup costs this many queries
RIPPLE_MINIMUM_RELEASE_AGE    default 3d; a repo may only raise it
RIPPLE_OPEN_PR_LIMIT          default 5; a repo may only lower it
RIPPLE_ALLOW_AUTOMERGE        default true; false forbids automerge everywhere
RIPPLE_WORKERS                default 4
RIPPLE_MAX_FILES              default 100000; a repo with more files is skipped with a warning
RIPPLE_MAX_DEPENDENCIES       default 5000; a repo with more dependencies is skipped with a warning
RIPPLE_WORK_DIR               checkout directory (default: OS temp dir)
RIPPLE_EVENT_WORKERS          webhook events handled at once (default 4)
RIPPLE_EVENT_WORKERS_PER_INSTALLATION  of which one installation may use (default 2)
RIPPLE_EVENT_BACKLOG          events stored and waiting before the receiver answers 503 (default 10000)
RIPPLE_EVENT_INSTALLATION_BACKLOG  the same cap for one installation (default 1000)
RIPPLE_EVENT_DEAD_RETENTION   how long an event that failed 3 times is kept for "ripple events" before it is purged (default 30d)
`

func isLoopback(u *url.URL) bool {
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func checkURL(name, v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil {
		return fmt.Errorf("%s: %q is not a valid URL without credentials", name, v)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u)) {
		return fmt.Errorf("%s: %q must use https (plain http is only accepted for loopback)", name, v)
	}
	return nil
}

// LoadServer reads the environment through getenv and validates every problem at once.
func LoadServer(getenv func(string) string) (*Server, error) {
	var errs []error
	str := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	dur := func(key, def string) time.Duration {
		d, err := ParseDuration(str(key, def))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
		return d
	}
	num := func(key string, def, lo int) int {
		n, err := strconv.Atoi(str(key, strconv.Itoa(def)))
		if err != nil || n < lo {
			errs = append(errs, fmt.Errorf("%s: want an integer >= %d", key, lo))
			return def
		}
		return n
	}
	shards := func(key string, def int) int {
		n := num(key, def, 1)
		if n > MaxDynamoShards {
			errs = append(errs, fmt.Errorf("%s: want an integer <= %d", key, MaxDynamoShards))
			return def
		}
		return n
	}
	s := &Server{
		Listen:                str("RIPPLE_LISTEN", ":8080"),
		WebhookSecret:         getenv("RIPPLE_WEBHOOK_SECRET"),
		GitHubAppID:           getenv("RIPPLE_GITHUB_APP_ID"),
		GitHubKeyFile:         getenv("RIPPLE_GITHUB_KEY_FILE"),
		GitHubAPIURL:          strings.TrimRight(str("RIPPLE_GITHUB_API_URL", "https://api.github.com"), "/"),
		GitHubGitURL:          strings.TrimRight(str("RIPPLE_GITHUB_GIT_URL", "https://github.com"), "/"),
		FeedURL:               strings.TrimRight(str("RIPPLE_FEED_URL", "https://index.golang.org"), "/"),
		GoReleasesURL:         strings.TrimRight(str("RIPPLE_GO_RELEASES_URL", "https://go.dev/dl/"), "/"),
		FeedInterval:          dur("RIPPLE_FEED_INTERVAL", "1m"),
		OSVURL:                strings.TrimRight(str("RIPPLE_OSV_URL", "https://api.osv.dev"), "/"),
		OSVDumpURL:            strings.TrimRight(str("RIPPLE_OSV_DUMP_URL", "https://storage.googleapis.com/osv-vulnerabilities"), "/"),
		OSVInterval:           dur("RIPPLE_OSV_INTERVAL", "10m"),
		OSVMaxFetch:           num("RIPPLE_OSV_MAX_FETCH", 100, 1),
		GoProxy:               strings.TrimRight(str("RIPPLE_GOPROXY", "https://proxy.golang.org"), "/"),
		GoProxyUser:           getenv("RIPPLE_GOPROXY_USER"),
		GoProxyToken:          getenv("RIPPLE_GOPROXY_TOKEN"),
		GoCC:                  getenv("RIPPLE_GO_CC"),
		Sandbox:               str("RIPPLE_SANDBOX", "none"),
		ContainerImage:        getenv("RIPPLE_CONTAINER_IMAGE"),
		ContainerCPUs:         getenv("RIPPLE_CONTAINER_CPUS"),
		ContainerMemory:       getenv("RIPPLE_CONTAINER_MEMORY"),
		ContainerPull:         getenv("RIPPLE_CONTAINER_PULL"),
		ContainerNetwork:      getenv("RIPPLE_CONTAINER_NETWORK"),
		Store:                 str("RIPPLE_STORE", "memory"),
		PostgresDSN:           getenv("RIPPLE_POSTGRES_DSN"),
		DynamoTable:           str("RIPPLE_DYNAMODB_TABLE", "ripple"),
		DynamoEndpoint:        getenv("RIPPLE_DYNAMODB_ENDPOINT"),
		DynamoJobShards:       shards("RIPPLE_DYNAMODB_JOB_SHARDS", DefaultDynamoJobShards),
		DynamoDependentShards: shards("RIPPLE_DYNAMODB_DEPENDENT_SHARDS", DefaultDynamoDependentShards),
		MinimumReleaseAge:     dur("RIPPLE_MINIMUM_RELEASE_AGE", "3d"),
		OpenPRLimit:           num("RIPPLE_OPEN_PR_LIMIT", 5, 1),
		AllowAutomerge:        true,
		Workers:               num("RIPPLE_WORKERS", 4, 1),
		WorkDir:               getenv("RIPPLE_WORK_DIR"),
		MaxFiles:              num("RIPPLE_MAX_FILES", DefaultMaxFiles, 1),
		MaxDependencies:       num("RIPPLE_MAX_DEPENDENCIES", DefaultMaxDependencies, 1),

		EventWorkers:                num("RIPPLE_EVENT_WORKERS", 4, 1),
		EventWorkersPerInstallation: num("RIPPLE_EVENT_WORKERS_PER_INSTALLATION", 2, 1),
		EventBacklog:                num("RIPPLE_EVENT_BACKLOG", 10_000, 1),
		EventInstallationBacklog:    num("RIPPLE_EVENT_INSTALLATION_BACKLOG", 1_000, 1),
		EventDeadRetention:          dur("RIPPLE_EVENT_DEAD_RETENTION", "30d"),
	}
	if v := getenv("RIPPLE_OSV"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, errors.New("RIPPLE_OSV: want true or false"))
		}
		s.OSV = b
	}
	if s.OSV {
		if s.OSVInterval <= 0 {
			errs = append(errs, errors.New("RIPPLE_OSV_INTERVAL must be positive"))
		}
		for k, v := range map[string]string{"RIPPLE_OSV_URL": s.OSVURL, "RIPPLE_OSV_DUMP_URL": s.OSVDumpURL} {
			if err := checkURL(k, v); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if v := getenv("RIPPLE_ALLOW_AUTOMERGE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, errors.New("RIPPLE_ALLOW_AUTOMERGE: want true or false"))
		}
		s.AllowAutomerge = b
	}
	for k, v := range map[string]string{"RIPPLE_WEBHOOK_SECRET": s.WebhookSecret, "RIPPLE_GITHUB_APP_ID": s.GitHubAppID, "RIPPLE_GITHUB_KEY_FILE": s.GitHubKeyFile} {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", k))
		}
	}
	if s.EventDeadRetention <= 0 {
		errs = append(errs, errors.New("RIPPLE_EVENT_DEAD_RETENTION must be positive"))
	}
	if s.FeedInterval <= 0 {
		errs = append(errs, errors.New("RIPPLE_FEED_INTERVAL must be positive"))
	}
	for k, v := range map[string]string{"RIPPLE_GITHUB_API_URL": s.GitHubAPIURL, "RIPPLE_GITHUB_GIT_URL": s.GitHubGitURL, "RIPPLE_FEED_URL": s.FeedURL, "RIPPLE_GO_RELEASES_URL": s.GoReleasesURL, "RIPPLE_GOPROXY": s.GoProxy} {
		if err := checkURL(k, v); err != nil {
			errs = append(errs, err)
		}
	}
	if u, err := url.Parse(s.GoProxy); s.GoProxyToken != "" && err == nil && u.Scheme != "https" {
		errs = append(errs, errors.New("RIPPLE_GOPROXY_TOKEN: the go command sends a credential only over https, so RIPPLE_GOPROXY must be an https URL"))
	}
	if s.GoProxyToken != "" && getenv("RIPPLE_GOPROXY") == "" {
		errs = append(errs, errors.New("RIPPLE_GOPROXY_TOKEN: set RIPPLE_GOPROXY to the private proxy; the credential is never sent to the default public proxy"))
	}
	switch s.Store {
	case "memory", "dynamodb":
	case "postgres":
		if s.PostgresDSN == "" {
			errs = append(errs, errors.New("RIPPLE_POSTGRES_DSN is required with RIPPLE_STORE=postgres"))
		}
	default:
		errs = append(errs, fmt.Errorf("RIPPLE_STORE: %q is not memory, postgres or dynamodb", s.Store))
	}
	if len(errs) > 0 {
		sortErrs(errs)
		return nil, errors.Join(errs...)
	}
	return s, nil
}

func sortErrs(e []error) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j].Error() < e[j-1].Error(); j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}
