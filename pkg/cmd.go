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
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func runProxyCmd(sig <-chan os.Signal) (bool, error) {
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", listenPort))
	if err != nil {
		return false, err
	}
	if err = l.Close(); err != nil {
		return false, err
	}

	log.Println("start a new connection to ssh server...")
	cmd := exec.CommandContext(context.Background(), "ssh",
		"-i",
		hostPrivKey,
		"-CND",
		strconv.FormatUint(listenPort, 10),
		fmt.Sprintf("%s@%s", hostUser, host),
		"-p",
		strconv.FormatUint(hostPort, 10),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Start(); err != nil {
		return false, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 2)

	var wg sync.WaitGroup
	go func() {
		wg.Add(1)
		defer wg.Done()
		if cmdErr := cmd.Wait(); err != nil {
			errs <- cmdErr
		} else {
			errs <- errors.New("ssh closed unexpectedly")
		}
	}()

	var proxyValidated atomic.Bool
	go func() {
		wg.Add(1)
		defer wg.Done()
		first := true
		var ticker *time.Ticker
		if validateDuration > 0 {
			ticker = time.NewTicker(validateDuration)
			defer ticker.Stop()
		}

		for {
			if waitErr := validProxy(ctx, validateTimeoutDuration); waitErr != nil {
				if ctx.Err() == nil {
					errs <- waitErr
				}
				return
			}

			if first {
				proxyValidated.Store(true)
				log.Println("connection established!")
				first = false
			} else {
				log.Println("proxy validated.")
			}

			if ticker == nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	select {
	case err = <-errs:
	case s := <-sig:
		log.Printf("receive signal: %s, exiting...\n", s)
	}

	_ = cmd.Cancel()
	cancel()
	wg.Wait()
	return proxyValidated.Load(), err
}
