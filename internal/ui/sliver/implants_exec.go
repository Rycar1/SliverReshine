package sliver

import (
	"context"
	"errors"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// Backdoor plants a payload in a file on the session (Windows only).
func (c *Client) Backdoor(sessionID, filePath, profileName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.Backdoor(ctx, &clientpb.BackdoorReq{
		FilePath:    filePath,
		ProfileName: profileName,
		Request:     &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// HijackDLL performs a DLL hijacking attack via the session (Windows only).
// ReferenceDLL and TargetDLL are optional in-memory payloads; when omitted the
// reference DLL is downloaded from the session's reference_dll_path.
func (c *Client) HijackDLL(sessionID, referenceDLLPath, targetLocation string, referenceDLL, targetDLL []byte, profileName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.HijackDLL(ctx, &clientpb.DllHijackReq{
		ReferenceDLLPath: referenceDLLPath,
		TargetLocation:   targetLocation,
		ReferenceDLL:     referenceDLL,
		TargetDLL:        targetDLL,
		ProfileName:      profileName,
		Request:          &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// ShellcodeRDIView is the JSON shape of converted RDI shellcode.
type ShellcodeRDIView struct {
	DataB64 string `json:"DataB64"`
	Size    int    `json:"Size"`
}

// ShellcodeRDI converts a DLL to position-independent shellcode (no session required).
func (c *Client) ShellcodeRDI(data []byte, functionName, arguments string) (*ShellcodeRDIView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.ShellcodeRDI(ctx, &clientpb.ShellcodeRDIReq{
		Data:         data,
		FunctionName: functionName,
		Arguments:    arguments,
	})
	if err != nil {
		return nil, err
	}
	return &ShellcodeRDIView{
		DataB64: encodeBase64(resp.Data),
		Size:    len(resp.Data),
	}, nil
}
