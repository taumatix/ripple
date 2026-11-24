package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The DynamoDB shard limits. They duplicate store.DefaultJobShards, store.DefaultDependentShards
// and store.MaxShards because importing the store here would pull every database driver into
// config; a test in package store keeps the two in step.
const (
	DefaultDynamoJobShards        = 8
	DefaultDynamoDependentShards  = 1
	DefaultDynamoQueryConcurrency = 4
	DefaultDynamoCounterStripes   = 1
	MaxDynamoShards               = 64
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
	// GitHubGraphQLURL overrides the GraphQL endpoint; empty means derived from
	// GitHubAPIURL (platform/github.GraphQLURL). GitHubAPIVersion pins
	// X-GitHub-Api-Version; empty sends the newest and falls back on 410.
	GitHubGraphQLURL string
	GitHubAPIVersion string
	GitHubGitURL     string
	// ActionsUpstreamURL is the GitHub API an action the GitHub host does not have is read
	// from (https://api.github.com for a GHES); empty reads the host only.
	// ActionsUpstreamToken lifts that API's anonymous rate limit and goes nowhere else.
	ActionsUpstreamURL, ActionsUpstreamToken string
	FeedURL                                  string
	// GoReleasesURL serves the Go release list (go-toolchain updates).
	GoReleasesURL string
	FeedInterval  time.Duration
	// OSV: advisories as an event source; off unless OSV is true.
	OSV           bool
	OSVURL        string
	OSVDumpURL    string
	OSVInterval   time.Duration
	OSVMaxFetch   int
	OSVSweepBatch int
	GoProxy       string
	// GoProxyUser and GoProxyToken are the credential for GoProxy (HTTP basic auth). They
	// reach only the go commands that run no repository code, and need RIPPLE_GOPROXY set to a private proxy. GoCC is the C compiler cgo
	// verification uses; empty is cc.
	GoProxyUser, GoProxyToken, GoCC string
	// RegistryAuthFile is a Docker config.json holding logins for private container
	// registries (ecosystem: docker); empty means every registry is read anonymously.
	RegistryAuthFile string
	// GoToolchainDir holds Go releases (go1.25.3/bin/go) that verification uses when a go.mod names one.
	GoToolchainDir string
	Sandbox        string // none, auto, bwrap, seatbelt, container, docker, podman
	// Container* configure the container, docker and podman sandboxes (docs/sandbox.md).
	ContainerImage, ContainerCPUs, ContainerMemory, ContainerPull, ContainerNetwork string
	// ContainerGoImages maps a Go minor ("1.26") to the image that carries it
	// (RIPPLE_CONTAINER_GO_IMAGES), so a go.mod naming that minor is verified by that Go.
	ContainerGoImages map[string]string
	// ContainerConfine makes the server create an internal runtime network and a proxy sidecar
	// at start, so a container's only way out is RIPPLE_GOPROXY (docker and podman).
	ContainerConfine bool
	Store            string // memory, postgres, dynamodb
	PostgresDSN      string
	DynamoTable      string
	DynamoEndpoint   string
	// DynamoJobShards and DynamoDependentShards spread the pending-job queue and each
	// package's dependents over that many index partitions (1..MaxDynamoShards).
	DynamoJobShards       int
	DynamoDependentShards int
	// DynamoQueryConcurrency bounds the shard Queries one read runs at once (1..MaxDynamoShards).
	DynamoQueryConcurrency int
	// DynamoAdaptiveDependents, when above zero, shards a package's dependents by how many it
	// has: the shard count doubles each time the package passes this many per shard. 0 is off.
	DynamoAdaptiveDependents int
	// DynamoCounterStripes spreads each package's dependents counter over this many items (1..MaxDynamoShards).
	DynamoCounterStripes int
	// DynamoScanPage and DynamoScanPause pace the whole-table Scans of `ripple dynamodb` and
	// the drift check: items read per page, and the wait between pages. Zero is unpaced.
	DynamoScanPage  int
	DynamoScanPause time.Duration
	// DynamoDriftCheck, when positive, has the server compare the dependents counters with
	// their index that often and log the gap. 0 is off.
	DynamoDriftCheck  time.Duration
	MinimumReleaseAge time.Duration
	OpenPRLimit       int
	AllowAutomerge    bool
	Workers           int
	WorkDir           string
	// MaxFiles and MaxDependencies cap one repo scan; a repo over either is skipped.
	MaxFiles        int
	MaxDependencies int
	// MaxBackfill bounds the jobs one scan creates for dependencies new to a repo; zero
	// means the engine's DefaultMaxBackfill. The unserved remainder carries to the next scan.
	MaxBackfill int
	// Event queue: workers overall and per installation, and the persisted backlog
	// overall and per installation (past either cap the receiver answers 503).
	EventWorkers                int
	EventWorkersPerInstallation int
	EventBacklog                int
	EventInstallationBacklog    int
	EventDeadRetention          time.Duration
	// WebhookSpool is "disk" (default) or "memory": where a signed webhook body over 64 KiB
	// waits for its signature check. See webhook.SpoolMode.
	WebhookSpool string
	// WebhookDedupeSignature (RIPPLE_WEBHOOK_DEDUPE_SIGNATURE, default false) also drops a
	// webhook whose verified signature was already handled; see webhook.DefaultDedupeBySignature.
	WebhookDedupeSignature bool
	// Replica names this process among the replicas sharing the store (empty: a random id)
	// and LeaseTTL is how long its claims on events and repos last without renewal.
	Replica  string
	LeaseTTL time.Duration
}

// Lease TTL bounds (RIPPLE_LEASE_TTL): the default, and the floor, which keeps the TTL well
// above the 5s clock-skew margin DynamoDB leases need (docs/replicas.md).
const (
	DefaultLeaseTTL = 30 * time.Second
	MinLeaseTTL     = 10 * time.Second
	maxReplicaName  = 64
)

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
RIPPLE_GITHUB_ENTERPRISE_URL  a GitHub Enterprise Server root such as https://ghes.example.com: sets the API
                              (/api/v3), GraphQL (/api/graphql) and git bases; the three below override it
RIPPLE_GITHUB_API_URL         default https://api.github.com (GHES: https://HOST/api/v3)
RIPPLE_GITHUB_GRAPHQL_URL     default derived from the API URL (GHES: https://HOST/api/graphql)
RIPPLE_GITHUB_API_VERSION     X-GitHub-Api-Version to pin (YYYY-MM-DD); default the newest ripple knows, lowered once on a 410
RIPPLE_GITHUB_GIT_URL         default https://github.com
RIPPLE_ACTIONS_UPSTREAM_URL   GitHub API to read an action's tags from when the GitHub host lacks it (GHES: https://api.github.com); default none
RIPPLE_ACTIONS_UPSTREAM_TOKEN token for RIPPLE_ACTIONS_UPSTREAM_URL only, to lift its anonymous rate limit; optional
RIPPLE_FEED_URL               default https://index.golang.org
RIPPLE_FEED_INTERVAL          default 1m
RIPPLE_GO_RELEASES_URL        Go release list for go-toolchain updates (default https://go.dev/dl/)
RIPPLE_OSV                    true turns on OSV security advisories as an event source (default false)
RIPPLE_OSV_URL                OSV API, default https://api.osv.dev
RIPPLE_OSV_DUMP_URL           OSV bucket, default https://storage.googleapis.com/osv-vulnerabilities
RIPPLE_OSV_INTERVAL           default 10m
RIPPLE_OSV_MAX_FETCH          advisory records fetched per poll (default 100)
RIPPLE_OSV_SWEEP_BATCH        repos re-checked per poll while a sweep runs (default 10)
RIPPLE_GOPROXY                module proxy used to verify major bumps (default https://proxy.golang.org)
RIPPLE_GOPROXY_USER           user for RIPPLE_GOPROXY (HTTP basic auth); with RIPPLE_GOPROXY_TOKEN, sent only to repos listing private-modules
RIPPLE_GOPROXY_TOKEN          password or token for RIPPLE_GOPROXY; needs an https proxy
RIPPLE_REGISTRY_AUTH_FILE     Docker config.json with logins for private container registries (ecosystem: docker); read once at start
RIPPLE_GO_TOOLCHAIN_DIR       directory of Go releases (go1.25.3/bin/go) verification runs with when a go.mod names one; never downloaded
RIPPLE_CONTAINER_GO_IMAGES    minor=image pairs (1.25=runner:1.25,1.26=runner:1.26): the container image that verifies a go.mod naming that Go minor
RIPPLE_GO_CC                  C compiler for repos with cgo: true (default: go's own, gcc or clang)
RIPPLE_STORE                  memory (default), postgres or dynamodb
RIPPLE_POSTGRES_DSN           required for postgres
RIPPLE_DYNAMODB_TABLE         default ripple (dynamodb)
RIPPLE_DYNAMODB_ENDPOINT      optional, for DynamoDB Local
RIPPLE_DYNAMODB_JOB_SHARDS    default 8 (1-64): partitions holding pending jobs; a due-jobs poll costs this many queries
RIPPLE_DYNAMODB_DEPENDENT_SHARDS  default 1 (1-64): partitions per package's dependents; each lookup costs this many queries
RIPPLE_DYNAMODB_QUERY_CONCURRENCY default 4 (1-64): shard queries one read runs at once; 1 reads shards in turn
RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS default 0 (off): dependents one partition may hold before a package gets more shards
RIPPLE_DYNAMODB_COUNTER_STRIPES default 1 (1-64): items each package's dependents counter is spread over; raise for a package imported by very many repos
RIPPLE_DYNAMODB_SCAN_PAGE     default 0 (unpaced): items one page of a whole-table scan (ripple dynamodb ..., the drift check) reads; set with the pause below
RIPPLE_DYNAMODB_SCAN_PAUSE_MS default 0: milliseconds to wait between those pages (and between packages), so a scan cannot use all the table's read capacity
RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL default 0 (off): how often the server compares the dependents counters with their index and logs the gap; read only
RIPPLE_MINIMUM_RELEASE_AGE    default 3d; a repo may only raise it
RIPPLE_OPEN_PR_LIMIT          default 5; a repo may only lower it
RIPPLE_ALLOW_AUTOMERGE        default true; false forbids automerge everywhere
RIPPLE_WORKERS                default 4
RIPPLE_MAX_FILES              default 100000; a repo with more files is skipped with a warning
RIPPLE_MAX_DEPENDENCIES       default 5000; a repo with more dependencies is skipped with a warning
RIPPLE_MAX_BACKFILL           default 10; jobs one scan creates for dependencies new to a repo, the rest carry to the next scan
RIPPLE_WORK_DIR               checkout directory (default: OS temp dir)
RIPPLE_EVENT_WORKERS          webhook events handled at once (default 4)
RIPPLE_EVENT_WORKERS_PER_INSTALLATION  of which one installation may use (default 2)
RIPPLE_EVENT_BACKLOG          events stored and waiting before the receiver answers 503 (default 10000)
RIPPLE_EVENT_INSTALLATION_BACKLOG  the same cap for one installation (default 1000)
RIPPLE_REPLICA                this process's name among replicas sharing one store (default: random per start); shows in logs
RIPPLE_LEASE_TTL              how long a replica's claim on an event or repo lasts without renewal, so how long a dead replica's
                              work waits for another (Go duration, default 30s, minimum 10s)
RIPPLE_WEBHOOK_SPOOL          disk (default) or memory: where a signed webhook body over 64 KiB waits for its signature check; memory never
                              writes a temp file but a forged body then costs up to 4 x 25 MB of memory instead of that much disk
RIPPLE_WEBHOOK_DEDUPE_SIGNATURE  true to also drop a webhook whose verified signature was already handled in the last hour, so a captured
                              body replayed under a new delivery id is dropped; a genuine redelivery of an identical body is dropped too
                              (default false; in memory per process, every signed host)
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

// checkConfine are the reasons RIPPLE_CONTAINER_CONFINE cannot work with the rest of the settings.
func checkConfine(s *Server) []error {
	var errs []error
	fail := func(msg string) { errs = append(errs, errors.New("RIPPLE_CONTAINER_CONFINE: "+msg)) }
	if s.Sandbox != "docker" && s.Sandbox != "podman" {
		fail("needs RIPPLE_SANDBOX=docker or podman (Apple's container has no internal networks)")
	}
	if s.ContainerNetwork != "" {
		fail("creates the network itself; unset RIPPLE_CONTAINER_NETWORK")
	}
	if u, err := url.Parse(s.GoProxy); err == nil && (u.Path != "" || u.RawQuery != "" || u.User != nil) {
		fail("the sidecar forwards to RIPPLE_GOPROXY, which must be scheme://host[:port] with no path or credentials")
	}
	if s.GoProxyToken != "" {
		fail("is not yet combined with RIPPLE_GOPROXY_TOKEN: go sends a credential only over https and the sidecar is http")
	}
	return errs
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

// CheckURL reports whether v is a usable base URL for the setting name: https, or http
// on loopback, and no credentials in it. Host builders validate their own settings with it.
func CheckURL(name, v string) error { return checkURL(name, v) }

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
	// numOpt is num for a setting whose absence means "the engine's default" (zero).
	numOpt := func(key string, lo int) int {
		if getenv(key) == "" {
			return 0
		}
		return num(key, lo, lo)
	}
	shards := func(key string, def int) int {
		n := num(key, def, 1)
		if n > MaxDynamoShards {
			errs = append(errs, fmt.Errorf("%s: want an integer <= %d", key, MaxDynamoShards))
			return def
		}
		return n
	}
	ghes := strings.TrimRight(getenv("RIPPLE_GITHUB_ENTERPRISE_URL"), "/")
	ghAPI, ghGit, ghGraphQL := "https://api.github.com", "https://github.com", ""
	if ghes != "" {
		ghAPI, ghGit, ghGraphQL = ghes+"/api/v3", ghes, ghes+"/api/graphql"
	}
	s := &Server{
		Listen:                   str("RIPPLE_LISTEN", ":8080"),
		WebhookSecret:            getenv("RIPPLE_WEBHOOK_SECRET"),
		GitHubAppID:              getenv("RIPPLE_GITHUB_APP_ID"),
		GitHubKeyFile:            getenv("RIPPLE_GITHUB_KEY_FILE"),
		GitHubAPIURL:             strings.TrimRight(str("RIPPLE_GITHUB_API_URL", ghAPI), "/"),
		GitHubGitURL:             strings.TrimRight(str("RIPPLE_GITHUB_GIT_URL", ghGit), "/"),
		GitHubGraphQLURL:         strings.TrimRight(str("RIPPLE_GITHUB_GRAPHQL_URL", ghGraphQL), "/"),
		GitHubAPIVersion:         getenv("RIPPLE_GITHUB_API_VERSION"),
		ActionsUpstreamURL:       strings.TrimRight(getenv("RIPPLE_ACTIONS_UPSTREAM_URL"), "/"),
		ActionsUpstreamToken:     getenv("RIPPLE_ACTIONS_UPSTREAM_TOKEN"),
		FeedURL:                  strings.TrimRight(str("RIPPLE_FEED_URL", "https://index.golang.org"), "/"),
		GoReleasesURL:            strings.TrimRight(str("RIPPLE_GO_RELEASES_URL", "https://go.dev/dl/"), "/"),
		FeedInterval:             dur("RIPPLE_FEED_INTERVAL", "1m"),
		OSVURL:                   strings.TrimRight(str("RIPPLE_OSV_URL", "https://api.osv.dev"), "/"),
		OSVDumpURL:               strings.TrimRight(str("RIPPLE_OSV_DUMP_URL", "https://storage.googleapis.com/osv-vulnerabilities"), "/"),
		OSVInterval:              dur("RIPPLE_OSV_INTERVAL", "10m"),
		OSVMaxFetch:              num("RIPPLE_OSV_MAX_FETCH", 100, 1),
		OSVSweepBatch:            num("RIPPLE_OSV_SWEEP_BATCH", 10, 1),
		GoProxy:                  strings.TrimRight(str("RIPPLE_GOPROXY", "https://proxy.golang.org"), "/"),
		GoProxyUser:              getenv("RIPPLE_GOPROXY_USER"),
		GoProxyToken:             getenv("RIPPLE_GOPROXY_TOKEN"),
		GoCC:                     getenv("RIPPLE_GO_CC"),
		RegistryAuthFile:         getenv("RIPPLE_REGISTRY_AUTH_FILE"),
		GoToolchainDir:           getenv("RIPPLE_GO_TOOLCHAIN_DIR"),
		Sandbox:                  str("RIPPLE_SANDBOX", "none"),
		ContainerImage:           getenv("RIPPLE_CONTAINER_IMAGE"),
		ContainerCPUs:            getenv("RIPPLE_CONTAINER_CPUS"),
		ContainerMemory:          getenv("RIPPLE_CONTAINER_MEMORY"),
		ContainerPull:            getenv("RIPPLE_CONTAINER_PULL"),
		ContainerNetwork:         getenv("RIPPLE_CONTAINER_NETWORK"),
		Store:                    str("RIPPLE_STORE", "memory"),
		PostgresDSN:              getenv("RIPPLE_POSTGRES_DSN"),
		DynamoTable:              str("RIPPLE_DYNAMODB_TABLE", "ripple"),
		DynamoEndpoint:           getenv("RIPPLE_DYNAMODB_ENDPOINT"),
		DynamoJobShards:          shards("RIPPLE_DYNAMODB_JOB_SHARDS", DefaultDynamoJobShards),
		DynamoDependentShards:    shards("RIPPLE_DYNAMODB_DEPENDENT_SHARDS", DefaultDynamoDependentShards),
		DynamoQueryConcurrency:   shards("RIPPLE_DYNAMODB_QUERY_CONCURRENCY", DefaultDynamoQueryConcurrency),
		DynamoAdaptiveDependents: num("RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS", 0, 0),
		DynamoCounterStripes:     shards("RIPPLE_DYNAMODB_COUNTER_STRIPES", DefaultDynamoCounterStripes),
		DynamoScanPage:           num("RIPPLE_DYNAMODB_SCAN_PAGE", 0, 0),
		DynamoScanPause:          time.Duration(num("RIPPLE_DYNAMODB_SCAN_PAUSE_MS", 0, 0)) * time.Millisecond,
		DynamoDriftCheck:         dur("RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL", "0"),
		MinimumReleaseAge:        dur("RIPPLE_MINIMUM_RELEASE_AGE", "3d"),
		OpenPRLimit:              num("RIPPLE_OPEN_PR_LIMIT", 5, 1),
		AllowAutomerge:           true,
		Workers:                  num("RIPPLE_WORKERS", 4, 1),
		WorkDir:                  getenv("RIPPLE_WORK_DIR"),
		MaxFiles:                 num("RIPPLE_MAX_FILES", DefaultMaxFiles, 1),
		MaxDependencies:          num("RIPPLE_MAX_DEPENDENCIES", DefaultMaxDependencies, 1),
		MaxBackfill:              numOpt("RIPPLE_MAX_BACKFILL", 1),

		EventWorkers:                num("RIPPLE_EVENT_WORKERS", 4, 1),
		EventWorkersPerInstallation: num("RIPPLE_EVENT_WORKERS_PER_INSTALLATION", 2, 1),
		EventBacklog:                num("RIPPLE_EVENT_BACKLOG", 10_000, 1),
		EventInstallationBacklog:    num("RIPPLE_EVENT_INSTALLATION_BACKLOG", 1_000, 1),
		EventDeadRetention:          dur("RIPPLE_EVENT_DEAD_RETENTION", "30d"),
		WebhookSpool:                str("RIPPLE_WEBHOOK_SPOOL", "disk"),
		Replica:                     getenv("RIPPLE_REPLICA"),
		LeaseTTL:                    DefaultLeaseTTL,
	}
	if v := getenv("RIPPLE_LEASE_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < MinLeaseTTL {
			errs = append(errs, fmt.Errorf("RIPPLE_LEASE_TTL: want a Go duration of at least %s, such as 30s", MinLeaseTTL))
		} else {
			s.LeaseTTL = d
		}
	}
	if len(s.Replica) > maxReplicaName {
		errs = append(errs, fmt.Errorf("RIPPLE_REPLICA: at most %d characters", maxReplicaName))
	}
	if v := getenv("RIPPLE_OSV"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, errors.New("RIPPLE_OSV: want true or false"))
		}
		s.OSV = b
	}
	if s.DynamoDriftCheck > 0 && s.Store != "dynamodb" {
		errs = append(errs, errors.New("RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL needs RIPPLE_STORE=dynamodb"))
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
	if v := getenv("RIPPLE_WEBHOOK_DEDUPE_SIGNATURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, errors.New("RIPPLE_WEBHOOK_DEDUPE_SIGNATURE: want true or false"))
		}
		s.WebhookDedupeSignature = b
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
	if s.WebhookSpool != "disk" && s.WebhookSpool != "memory" {
		errs = append(errs, fmt.Errorf("RIPPLE_WEBHOOK_SPOOL: %q is not disk or memory", s.WebhookSpool))
	}
	if s.EventDeadRetention <= 0 {
		errs = append(errs, errors.New("RIPPLE_EVENT_DEAD_RETENTION must be positive"))
	}
	if s.FeedInterval <= 0 {
		errs = append(errs, errors.New("RIPPLE_FEED_INTERVAL must be positive"))
	}
	if ghes != "" {
		if err := checkURL("RIPPLE_GITHUB_ENTERPRISE_URL", ghes); err != nil {
			errs = append(errs, err)
		}
	}
	if s.ActionsUpstreamURL != "" {
		if err := checkURL("RIPPLE_ACTIONS_UPSTREAM_URL", s.ActionsUpstreamURL); err != nil {
			errs = append(errs, err)
		}
	}
	if s.ActionsUpstreamToken != "" && s.ActionsUpstreamURL == "" {
		errs = append(errs, errors.New("RIPPLE_ACTIONS_UPSTREAM_TOKEN: set RIPPLE_ACTIONS_UPSTREAM_URL; the token is sent nowhere else"))
	}
	if s.GitHubGraphQLURL != "" {
		if err := checkURL("RIPPLE_GITHUB_GRAPHQL_URL", s.GitHubGraphQLURL); err != nil {
			errs = append(errs, err)
		}
	}
	if v := s.GitHubAPIVersion; v != "" {
		if _, err := time.Parse("2006-01-02", v); err != nil {
			errs = append(errs, fmt.Errorf("RIPPLE_GITHUB_API_VERSION: %q is not a YYYY-MM-DD version", v))
		}
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
	if d := s.GoToolchainDir; d != "" {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() || !filepath.IsAbs(d) {
			errs = append(errs, errors.New("RIPPLE_GO_TOOLCHAIN_DIR: must be an absolute path to an existing directory"))
		}
	}
	if v := getenv("RIPPLE_CONTAINER_CONFINE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, errors.New("RIPPLE_CONTAINER_CONFINE: want true or false"))
		}
		s.ContainerConfine = b
	}
	if s.ContainerConfine {
		errs = append(errs, checkConfine(s)...)
	}
	if v := getenv("RIPPLE_CONTAINER_GO_IMAGES"); v != "" {
		m, err := parseGoImages(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("RIPPLE_CONTAINER_GO_IMAGES: %w", err))
		}
		s.ContainerGoImages = m
		if s.Sandbox != "container" && s.Sandbox != "docker" && s.Sandbox != "podman" {
			errs = append(errs, errors.New("RIPPLE_CONTAINER_GO_IMAGES: only a container, docker or podman sandbox runs an image; with another sandbox use RIPPLE_GO_TOOLCHAIN_DIR"))
		}
	}
	if s.GoToolchainDir != "" && (s.Sandbox == "container" || s.Sandbox == "docker" || s.Sandbox == "podman") {
		errs = append(errs, errors.New("RIPPLE_GO_TOOLCHAIN_DIR: a container backend runs the Go its image carries; the directory would be ignored, so set it only with another sandbox (or choose images with RIPPLE_CONTAINER_GO_IMAGES)"))
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

var goMinorKey = regexp.MustCompile(`^1\.[0-9]+$`)

// parseGoImages reads "1.25=img,1.26=img": a Go minor and the image that carries it.
func parseGoImages(v string) (map[string]string, error) {
	out := map[string]string{}
	for _, el := range strings.Split(v, ",") {
		minor, img, ok := strings.Cut(strings.TrimSpace(el), "=")
		if !ok || !goMinorKey.MatchString(minor) || img == "" || strings.ContainsAny(img, " \t\n") || strings.HasPrefix(img, "-") {
			return nil, fmt.Errorf("%q: want <go minor>=<image>, such as 1.26=runner:1.26", el)
		}
		if _, dup := out[minor]; dup {
			return nil, fmt.Errorf("go %s is given twice", minor)
		}
		out[minor] = img
	}
	return out, nil
}
