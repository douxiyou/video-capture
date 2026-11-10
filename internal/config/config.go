package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

type Config struct {
	Server   Server   `mapstructure:"server"`
	Database Database `mapstructure:"database"`
	Face     Face     `mapstructure:"face"`
	Auth     Auth     `mapstructure:"auth"`
	OSS      OSS      `mapstructure:"oss"`
}

type OSS struct {
	Enabled         bool   `mapstructure:"enabled"`
	Region          string `mapstructure:"region"`
	Bucket          string `mapstructure:"bucket"`
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	SessionToken    string `mapstructure:"session_token"`
}

type Server struct {
	Addr string `mapstructure:"addr"`
}

type Database struct {
	Path string `mapstructure:"path"`
}

type Face struct {
	Enabled       bool    `mapstructure:"enabled"`
	ModelPath     string  `mapstructure:"model_path"`
	ONNXLibrary   string  `mapstructure:"onnx_library"`
	Threshold     float64 `mapstructure:"threshold"`
	EncryptionKey string  `mapstructure:"encryption_key"`
}

type Auth struct {
	JWTSigningKey string `mapstructure:"jwt_signing_key"`
}

// Load reads a YAML file, with secrets supplied by environment variables when set.
func Load(path string, explicit bool) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("configuration file path must not be empty")
	}
	v := viper.New()
	v.AllowEmptyEnv(true)
	v.SetConfigType("yaml")
	v.SetConfigFile(path)
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("database.path", "video-capture.db")
	v.SetDefault("face.enabled", true)
	v.SetDefault("face.model_path", "mobile_facenet-onnx-float/mobile_facenet.onnx")
	v.SetDefault("face.onnx_library", "")
	v.SetDefault("face.threshold", 0.6)
	v.SetDefault("face.encryption_key", "")
	v.SetDefault("auth.jwt_signing_key", "")
	v.SetDefault("oss.enabled", false)
	v.SetDefault("oss.region", "")
	v.SetDefault("oss.bucket", "")
	v.SetDefault("oss.endpoint", "")
	v.SetDefault("oss.access_key_id", "")
	v.SetDefault("oss.access_key_secret", "")
	v.SetDefault("oss.session_token", "")
	if err := v.BindEnv("face.encryption_key", "FACE_ENCRYPTION_KEY"); err != nil {
		return Config{}, fmt.Errorf("bind face encryption key: %w", err)
	}
	if err := v.BindEnv("auth.jwt_signing_key", "JWT_SIGNING_KEY"); err != nil {
		return Config{}, fmt.Errorf("bind JWT signing key: %w", err)
	}
	for _, binding := range [][]string{
		{"oss.access_key_id", "OSS_ACCESS_KEY_ID", "ALIYUN_ACCESS_KEY_ID"},
		{"oss.access_key_secret", "OSS_ACCESS_KEY_SECRET", "ALIYUN_ACCESS_KEY_SECRET"},
		{"oss.session_token", "OSS_SESSION_TOKEN"},
	} {
		if err := v.BindEnv(binding...); err != nil {
			return Config{}, fmt.Errorf("bind %s: %w", binding[0], err)
		}
	}
	if err := v.ReadInConfig(); err != nil && (explicit || !os.IsNotExist(err)) {
		return Config{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	var cfg Config
	if err := v.UnmarshalExact(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.WeaklyTypedInput = false
	}); err != nil {
		return Config{}, fmt.Errorf("decode configuration %q: %w", path, err)
	}
	if strings.TrimSpace(cfg.Server.Addr) == "" {
		return Config{}, fmt.Errorf("server.addr must not be empty")
	}
	if strings.TrimSpace(cfg.Database.Path) == "" {
		return Config{}, fmt.Errorf("database.path must not be empty")
	}
	if cfg.OSS.Enabled && (!cfg.Face.Enabled || strings.TrimSpace(cfg.OSS.Region) == "" || strings.TrimSpace(cfg.OSS.Bucket) == "") {
		return Config{}, fmt.Errorf("oss.enabled requires face.enabled, oss.region and oss.bucket")
	}
	return cfg, nil
}
