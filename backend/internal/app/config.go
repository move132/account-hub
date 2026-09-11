package app

import "time"

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
	DisplayTimezone          string
	AdminSessionTTL          time.Duration
	UpstreamRequestTimeout   time.Duration
	UpstreamRetryCount       int
	RefreshWorkerConcurrency int
	JobScanInterval          time.Duration
}

func LoadConfig() Config {
	return Config{
		ListenAddress:            ListenAddress,
		DatabasePath:             DatabasePath,
		AdminUsername:            AdminUsername,
		DisplayTimezone:          DisplayTimezone,
		AdminSessionTTL:          AdminSessionTTL,
		UpstreamRequestTimeout:   UpstreamRequestTimeout,
		UpstreamRetryCount:       UpstreamRetryCount,
		RefreshWorkerConcurrency: RefreshWorkerConcurrency,
		JobScanInterval:          JobScanInterval,
	}
}
