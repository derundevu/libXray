package xray

import (
	"context"
	"errors"
	"time"

	"github.com/xtls/libxray/nodep"
)

// Ping Xray config and find the delay and country code of its outbound.
// datDir means the dir which geosite.dat and geoip.dat are in.
// configPath means the config.json file path.
// timeout means how long the http request will be cancelled if no response, in units of seconds.
// url means the website we use to test speed. "https://www.google.com" is a good choice for most cases.
// proxy means the local http/socks5 proxy, like "socks5://[::1]:1080".
func Ping(datDir string, configPath string, timeout int, url string, proxy string) (int64, error) {
	return PingWithType(datDir, configPath, timeout, url, proxy, "head")
}

func PingWithType(datDir string, configPath string, timeout int, url string, proxy string, pingType string) (int64, error) {
	return PingWithContext(context.Background(), datDir, configPath, timeout, url, proxy, pingType)
}

func PingWithContext(ctx context.Context, datDir string, configPath string, timeout int, url string, proxy string, pingType string) (int64, error) {
	if timeout <= 0 {
		return nodep.PingDelayError, errors.New("ping timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second*time.Duration(timeout))
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nodep.PingDelayError, err
	}
	InitEnv(datDir)
	server, err := StartXray(configPath)
	if err != nil {
		return nodep.PingDelayError, err
	}
	defer server.Close()

	if err := server.Start(); err != nil {
		return nodep.PingDelayError, err
	}

	delay, err := nodep.MeasureDelayWithContext(ctx, timeout, url, proxy, pingType)
	if err != nil {
		return delay, err
	}

	return delay, nil
}
