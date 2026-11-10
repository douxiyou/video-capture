package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.yaml")
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsAndMissingFile(t *testing.T) {
	t.Setenv("FACE_ENCRYPTION_KEY", "")
	t.Setenv("JWT_SIGNING_KEY", "")
	path := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != ":8080" || cfg.Database.Path != "video-capture.db" ||
		!cfg.Face.Enabled || cfg.Face.Threshold != 0.6 || cfg.Face.ModelPath != "mobile_facenet-onnx-float/mobile_facenet.onnx" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if _, err := Load(path, true); err == nil {
		t.Fatal("expected missing explicit file to fail")
	}
}

func TestExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != "video-capture.db" || cfg.Server.Addr != ":8080" {
		t.Fatal("example configuration does not match expected defaults")
	}
}

func TestYAMLAndEnvironmentPrecedence(t *testing.T) {
	t.Setenv("FACE_ENCRYPTION_KEY", "environment-face")
	t.Setenv("JWT_SIGNING_KEY", "environment-jwt")
	path := configFile(t, `server:
  addr: "127.0.0.1:8081"
database:
  path: "other.db"
face:
  enabled: false
  model_path: "face.onnx"
  onnx_library: "runtime.so"
  threshold: 0.75
  encryption_key: "yaml-face"
auth:
  jwt_signing_key: "yaml-jwt"
`)
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != "127.0.0.1:8081" || cfg.Database.Path != "other.db" || cfg.Face.Enabled ||
		cfg.Face.ModelPath != "face.onnx" || cfg.Face.ONNXLibrary != "runtime.so" || cfg.Face.Threshold != 0.75 ||
		cfg.Face.EncryptionKey != "environment-face" || cfg.Auth.JWTSigningKey != "environment-jwt" {
		t.Fatal("configuration did not use expected YAML values and environment overrides")
	}
}

func TestEmptyEnvironmentOverridesFile(t *testing.T) {
	t.Setenv("FACE_ENCRYPTION_KEY", "")
	t.Setenv("JWT_SIGNING_KEY", "")
	path := configFile(t, "face:\n  encryption_key: yaml-face\nauth:\n  jwt_signing_key: yaml-jwt\n")
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Face.EncryptionKey != "" || cfg.Auth.JWTSigningKey != "" {
		t.Fatal("empty environment values should override the file")
	}
}

func TestOSSCredentialsFromYAMLAndEnvironment(t *testing.T) {
	for _, name := range []string{"OSS_ACCESS_KEY_ID", "ALIYUN_ACCESS_KEY_ID", "OSS_ACCESS_KEY_SECRET", "ALIYUN_ACCESS_KEY_SECRET", "OSS_SESSION_TOKEN"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	path := configFile(t, `oss:
  access_key_id: yaml-id
  access_key_secret: yaml-secret
  session_token: yaml-token
`)
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OSS.AccessKeyID != "yaml-id" || cfg.OSS.AccessKeySecret != "yaml-secret" || cfg.OSS.SessionToken != "yaml-token" {
		t.Fatal("OSS credentials should come from YAML")
	}
	t.Setenv("ALIYUN_ACCESS_KEY_ID", "aliyun-id")
	t.Setenv("ALIYUN_ACCESS_KEY_SECRET", "aliyun-secret")
	t.Setenv("OSS_SESSION_TOKEN", "env-token")
	cfg, err = Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OSS.AccessKeyID != "aliyun-id" || cfg.OSS.AccessKeySecret != "aliyun-secret" || cfg.OSS.SessionToken != "env-token" {
		t.Fatal("environment credentials should override YAML")
	}
	t.Setenv("OSS_ACCESS_KEY_ID", "oss-id")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "")
	cfg, err = Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OSS.AccessKeyID != "oss-id" || cfg.OSS.AccessKeySecret != "" {
		t.Fatal("OSS environment variables, including empty values, should take precedence")
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, value := range []string{
		"face: [broken",
		"face:\n  enabled: not-a-bool\n",
		"face:\n  threshold: invalid\n",
		"database:\n  path: ''\n",
		"server:\n  addr: ''\n",
		"unknown_key: 1\n",
		"oss:\n  enabled: true\n",
		"face:\n  enabled: false\noss:\n  enabled: true\n  region: cn-hangzhou\n  bucket: example\n",
	} {
		t.Run(strings.ReplaceAll(value, "\n", "_"), func(t *testing.T) {
			if _, err := Load(configFile(t, value), true); err == nil {
				t.Fatal("expected invalid configuration to fail")
			}
		})
	}
}
