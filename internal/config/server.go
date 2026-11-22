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

// Server is the service-wide configuration, read from the environment. Repo files
// can tighten the limits here, never widen them. Every URL comes from here and
// never from a repo file, so a repo cannot point ripple at an internal host.
type Server struct {
	Listen            string
	WebhookSecret     string
	GitHubAppID       string
	GitHubKeyFile     string
	GitHubAPIURL      string
	GitHubGitURL      string
	FeedURL           string
	FeedInterval      time.Duration
	GoProxy           string
	Store             string // memory, postgres, dynamodb
	PostgresDSN       string
	DynamoTable       string
	DynamoEndpoint    string
	MinimumReleaseAge time.Duration
	OpenPRLimit       int
	AllowAutomerge    bool
	Workers           int
	WorkDir           string
}

// ServerEnv documents every variable; `ripple config validate --server` prints it.
const ServerEnv = `RIPPLE_LISTEN                 listen address (default :8080)
RIPPLE_WEBHOOK_SECRET         GitHub App webhook secret (required)
RIPPLE_GITHUB_APP_ID          GitHub App id (required)
RIPPLE_GITHUB_KEY_FILE        path to the App private key PEM (required)
RIPPLE_GITHUB_API_URL         default https://api.github.com
RIPPLE_GITHUB_GIT_URL         default https://github.com
RIPPLE_FEED_URL               default https://index.golang.org
RIPPLE_FEED_INTERVAL          default 1m
RIPPLE_GOPROXY                module proxy used to verify major bumps (default https://proxy.golang.org)
RIPPLE_STORE                  memory (default), postgres or dynamodb
RIPPLE_POSTGRES_DSN           required for postgres
RIPPLE_DYNAMODB_TABLE         default ripple (dynamodb)
RIPPLE_DYNAMODB_ENDPOINT      optional, for DynamoDB Local
RIPPLE_MINIMUM_RELEASE_AGE    default 3d; a repo may only raise it
RIPPLE_OPEN_PR_LIMIT          default 5; a repo may only lower it
RIPPLE_ALLOW_AUTOMERGE        default true; false forbids automerge everywhere
RIPPLE_WORKERS                default 4
RIPPLE_WORK_DIR               checkout directory (default: OS temp dir)
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
	s := &Server{
		Listen:            str("RIPPLE_LISTEN", ":8080"),
		WebhookSecret:     getenv("RIPPLE_WEBHOOK_SECRET"),
		GitHubAppID:       getenv("RIPPLE_GITHUB_APP_ID"),
		GitHubKeyFile:     getenv("RIPPLE_GITHUB_KEY_FILE"),
		GitHubAPIURL:      strings.TrimRight(str("RIPPLE_GITHUB_API_URL", "https://api.github.com"), "/"),
		GitHubGitURL:      strings.TrimRight(str("RIPPLE_GITHUB_GIT_URL", "https://github.com"), "/"),
		FeedURL:           strings.TrimRight(str("RIPPLE_FEED_URL", "https://index.golang.org"), "/"),
		FeedInterval:      dur("RIPPLE_FEED_INTERVAL", "1m"),
		GoProxy:           strings.TrimRight(str("RIPPLE_GOPROXY", "https://proxy.golang.org"), "/"),
		Store:             str("RIPPLE_STORE", "memory"),
		PostgresDSN:       getenv("RIPPLE_POSTGRES_DSN"),
		DynamoTable:       str("RIPPLE_DYNAMODB_TABLE", "ripple"),
		DynamoEndpoint:    getenv("RIPPLE_DYNAMODB_ENDPOINT"),
		MinimumReleaseAge: dur("RIPPLE_MINIMUM_RELEASE_AGE", "3d"),
		OpenPRLimit:       num("RIPPLE_OPEN_PR_LIMIT", 5, 1),
		AllowAutomerge:    true,
		Workers:           num("RIPPLE_WORKERS", 4, 1),
		WorkDir:           getenv("RIPPLE_WORK_DIR"),
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
	if s.FeedInterval <= 0 {
		errs = append(errs, errors.New("RIPPLE_FEED_INTERVAL must be positive"))
	}
	for k, v := range map[string]string{"RIPPLE_GITHUB_API_URL": s.GitHubAPIURL, "RIPPLE_GITHUB_GIT_URL": s.GitHubGitURL, "RIPPLE_FEED_URL": s.FeedURL, "RIPPLE_GOPROXY": s.GoProxy} {
		if err := checkURL(k, v); err != nil {
			errs = append(errs, err)
		}
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
