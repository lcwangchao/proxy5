// Copyright 2024 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/net/proxy"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	host                    string
	hostPort                uint64
	hostUser                string
	hostPrivKey             string
	listenPort              uint64
	validateInterval        string
	validateDuration        time.Duration
	validateTimeout         string
	validateTimeoutDuration time.Duration
)

func init() {
	flag.StringVar(&host, "host", "", "host address")
	flag.Uint64Var(&hostPort, "port", 22, "host port")
	flag.StringVar(&hostUser, "user", "", "host user")
	flag.StringVar(&hostPrivKey, "key", "", "host private key")
	flag.Uint64Var(&listenPort, "l", 7070, "local listen port")
	flag.StringVar(&validateInterval, "validate-interval", "", "proxy validate interval")
	flag.StringVar(&validateTimeout, "validate-timeout", "15s", "proxy validate timeout")
}

func exit(msg string, printUsage bool) {
	_, _ = fmt.Fprintln(os.Stderr, msg)
	if printUsage {
		flag.Usage()
	}
	os.Exit(-1)
}

func validateArgs() {
	flag.Parse()

	if host == "" {
		exit("host is required", true)
	}
	if hostUser == "" {
		exit("user is required", true)
	}
	if hostPrivKey == "" {
		exit("host private key is required", true)
	}
	if validateInterval != "" {
		d, err := time.ParseDuration(validateInterval)
		if err != nil {
			exit(err.Error(), true)
		}
		if d < time.Second {
			exit("validate interval should be greater than 1 second", true)
		}
		validateDuration = d
	}

	d, err := time.ParseDuration(validateTimeout)
	if err != nil {
		exit(err.Error(), true)
	}
	validateTimeoutDuration = d
}

func main() {
	validateArgs()
	firstOpen := true
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	var lastOpen time.Time
loop:
	for {
		lastOpen = time.Now()
		validated, err := runProxyCmd(sig)
		if err == nil {
			break loop
		}

		if firstOpen && !validated {
			exit(err.Error(), false)
		}

		firstOpen = false
		if time.Since(lastOpen) >= time.Minute {
			log.Printf("error: %s, reconnect...\n", err)
			continue
		}

		retrySec := 10
		log.Printf("error: %s, reconnect after %d seconds...\n", err, retrySec)
		select {
		case <-time.After(time.Duration(retrySec) * time.Second):
		case s := <-sig:
			log.Printf("receive signal: %s, exiting...\n", s)
			break loop
		}
	}
	log.Println("exit.")
}

func validProxy(ctx context.Context, timeout time.Duration) error {
	start := time.Now()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if time.Since(start) > timeout {
			return errors.New("proxy is invalid")
		}

		time.Sleep(2 * time.Second)
		dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort), nil, proxy.Direct)
		if err != nil {
			return err
		}

		httpTransport := &http.Transport{}
		httpClient := &http.Client{Transport: httpTransport, Timeout: 15 * time.Second}
		httpTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.(proxy.ContextDialer).DialContext(ctx, network, addr)
		}

		resp, err := httpClient.Get("https://www.google.com")
		if err != nil {
			continue
		}

		_ = resp.Body.Close()
		return nil
	}
}
