// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"
)

// Tests named in development/platform/platform_plan.md PLT-002.

func TestConfigCollectsAllErrors(t *testing.T) {
	_, err := Load(env(map[string]string{"FD_SECRET_KEY": key32, "FD_DB_POOL": "x", "FD_LOG_FORMAT": "xml", "FD_SHUTDOWN_TIMEOUT": "soon"}))
	le, ok := err.(*LoadError)
	if !ok || len(le.Errs) != 3 {
		t.Fatalf("want 3 errors, got %v", err)
	}
}

func TestConfigRedactsSecrets(t *testing.T) {
	c, err := Load(env(map[string]string{"FD_SECRET_KEY": key32, "FD_METRICS_TOKEN": "tok-123", "FD_DATABASE_URL": "postgres://a:pw9@h/db"}))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for k, v := range c.Redacted() {
		all += k + "=" + v + "\n"
	}
	for _, secret := range []string{"tok-123", "pw9", key32} {
		if strings.Contains(all, secret) {
			t.Errorf("secret %q leaked", secret)
		}
	}
}

func TestConfigRejectsUnknownDBScheme(t *testing.T) {
	_, err := Load(env(map[string]string{"FD_SECRET_KEY": key32, "FD_DATABASE_URL": "mysql://x"}))
	if err == nil || !strings.Contains(err.Error(), "FD_DATABASE_URL") {
		t.Fatalf("got %v", err)
	}
}

func TestConfigS3RequiresKeys(t *testing.T) {
	_, err := Load(env(map[string]string{"FD_SECRET_KEY": key32, "FD_BLOB_URL": "s3://bucket/p?region=x"}))
	if err == nil || !strings.Contains(err.Error(), "FD_S3_ACCESS_KEY") {
		t.Fatalf("got %v", err)
	}
	if _, err := Load(env(map[string]string{"FD_SECRET_KEY": key32, "FD_BLOB_URL": "s3://bucket/p", "FD_S3_ACCESS_KEY": "a", "FD_S3_SECRET_KEY": "b"})); err != nil {
		t.Fatal(err)
	}
}
