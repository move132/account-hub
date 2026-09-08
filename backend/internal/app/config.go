package app

import (
	"os"
	"time"
)

const (
	ListenAddress            = "0.0.0.0:8500"
	DatabasePath             = "./data/account-hub.db"
	AdminUsername            = "admin"
	DisplayTimezone          = "Asia/Shanghai"
	AdminSessionTTL          = 24 * time.Hour
	UpstreamRequestTimeout   = 30 * time.Second
	UpstreamRetryCount       = 2
	RefreshWorkerConcurrency = 1
	JobScanInterval          = 5 * time.Second
)

type Config struct {
	ListenAddress            string
	DatabasePath             string
	AdminUsername            string
	AdminPassword            string
	AdminPasswordManaged     bool
	DisplayTimezone          string
	AdminSessionTTL          time.Duration
	UpstreamRequestTimeout   time.Duration
	UpstreamRetryCount       int
	RefreshWorkerConcurrency int
	JobScanInterval          time.Duration
}

func LoadConfig() Config {
	password, managed := os.LookupEnv("ADMIN_PASSWORD")
	return Config{
		ListenAddress:            ListenAddress,
		DatabasePath:             DatabasePath,
		AdminUsername:            AdminUsername,
		AdminPassword:            password,
		AdminPasswordManaged:     managed,
		DisplayTimezone:          DisplayTimezone,
		AdminSessionTTL:          AdminSessionTTL,
		UpstreamRequestTimeout:   UpstreamRequestTimeout,
		UpstreamRetryCount:       UpstreamRetryCount,
		RefreshWorkerConcurrency: RefreshWorkerConcurrency,
		JobScanInterval:          JobScanInterval,
	}
}
