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
	"os/signal"
	"syscall"

	"github.com/armon/go-socks5"
	"golang.org/x/crypto/ssh"
)

func openNativeProxy() (err error) {
	// read private key file
	pemBytes, err := os.ReadFile(hostPrivKey)
	if err != nil {
		exit(err.Error(), false)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		exit(err.Error(), false)
	}

	hostAddr := fmt.Sprintf("%s:%d", host, hostPort)
	listenAddr := fmt.Sprintf("localhost:%d", listenPort)
	sshConf := &ssh.ClientConfig{
		User:            hostUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	log.Println("connecting to ssh server...")
	sshConn, err := ssh.Dial("tcp", hostAddr, sshConf)
	if err != nil {
		return err
	}

	log.Println("ssh server connected!")
	defer func() {
		_ = sshConn.Close()
	}()

	log.Println("starting socks5 proxy...")
	socksConf := &socks5.Config{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return sshConn.Dial(network, addr)
		},
		Resolver: &proxyDNSResolver{},
	}
	socks, err := socks5.New(socksConf)
	if err != nil {
		return err
	}

	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	log.Println("socks5 proxy started!")
	defer func() {
		_ = l.Close()
	}()

	sshErr := make(chan error, 1)
	go func() {
		sshErr <- sshConn.Wait()
		close(sshErr)
	}()

	socksErr := make(chan error, 1)
	go func() {
		socksErr <- socks.Serve(l)
		close(socksErr)
	}()

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err = <-sshErr:
	case err = <-socksErr:
	case <-ch:
		return nil
	}

	if err == nil {
		err = errors.New("chan closed unexpectedly")
	}
	return err
}

type proxyDNSResolver struct{}

func (r *proxyDNSResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, network, "8.8.8.8:53")
		},
	}
	ips, err := resolver.LookupIP(ctx, "ip", name)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, ips[0], nil

}
