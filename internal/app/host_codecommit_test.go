package app

import (
	"context"
	"testing"
)

func TestBuildCodeCommitFromEnv(t *testing.T) {
	const topic = "arn:aws:sns:us-east-2:123456789012:ripple"
	build := func(env map[string]string) (bool, error) {
		h, err := buildCodeCommit(context.Background(), HostDeps{Getenv: func(k string) string { return env[k] }})
		if err == nil && h != nil && h.Name != "codecommit" {
			t.Errorf("host %+v", h)
		}
		return h != nil, err
	}
	if on, err := build(nil); on || err != nil {
		t.Errorf("unset: %v %v", on, err)
	}
	keys := map[string]string{"RIPPLE_CODECOMMIT_ACCESS_KEY_ID": "AK", "RIPPLE_CODECOMMIT_SECRET_ACCESS_KEY": "s"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range keys {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	if _, err := build(with("RIPPLE_CODECOMMIT_REGIONS", "us-east-2")); err == nil {
		t.Error("regions without topics accepted")
	}
	if _, err := build(with("RIPPLE_CODECOMMIT_REGIONS", "us-east-2,evil.com#", "RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS", topic)); err == nil {
		t.Error("bad region accepted")
	}
	if _, err := build(map[string]string{"RIPPLE_CODECOMMIT_REGIONS": "us-east-2", "RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS": topic,
		"RIPPLE_CODECOMMIT_ACCESS_KEY_ID": "AK"}); err == nil {
		t.Error("half a key pair accepted")
	}
	if on, err := build(with("RIPPLE_CODECOMMIT_REGIONS", " us-east-2 , eu-west-1", "RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS", topic,
		"RIPPLE_CODECOMMIT_SESSION_TOKEN", "t")); !on || err != nil {
		t.Errorf("valid: %v %v", on, err)
	}
}
