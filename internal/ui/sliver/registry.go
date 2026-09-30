package sliver

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// RegReadResult is the JSON shape of a registry read.
type RegReadResult struct {
	Value string `json:"Value"`
}

// RegistryRead reads a registry value on a windows session.
func (c *Client) RegistryRead(sessionID, hive, path, key string) (*RegReadResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.RegistryRead(ctx, &sliverpb.RegistryReadReq{
		Hive:    hive,
		Path:    path,
		Key:     key,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return &RegReadResult{Value: resp.Value}, nil
}

// RegistryWrite writes a registry value on a windows session.
// valueType: "string" (default), "dword", "qword"
// (Sliver v1.15.16 的 RegistryWriteReq 不含 BinaryValue 字段)
func (c *Client) RegistryWrite(sessionID, hive, path, key, value, valueType string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	req := &sliverpb.RegistryWriteReq{
		Hive:    hive,
		Path:    path,
		Key:     key,
		Request: &commonpb.Request{SessionID: sessionID},
	}
	switch valueType {
	case "dword":
		n, _ := strconv.ParseUint(value, 10, 32)
		req.DWordValue = uint32(n)
	case "qword":
		n, _ := strconv.ParseUint(value, 10, 64)
		req.QWordValue = n
	default:
		req.StringValue = value
	}
	resp, err := c.RPC.RegistryWrite(ctx, req)
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// RegistryListSubKeys lists subkeys under a registry path on a windows session.
func (c *Client) RegistryListSubKeys(sessionID, hive, path string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.RegistryListSubKeys(ctx, &sliverpb.RegistrySubKeyListReq{
		Hive:    hive,
		Path:    path,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	keys := resp.Subkeys
	if keys == nil {
		keys = []string{}
	}
	sort.Strings(keys)
	return keys, nil
}

// RegistryListValues lists value names under a registry path on a windows session.
func (c *Client) RegistryListValues(sessionID, hive, path string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.RegistryListValues(ctx, &sliverpb.RegistryListValuesReq{
		Hive:    hive,
		Path:    path,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	names := resp.ValueNames
	if names == nil {
		names = []string{}
	}
	sort.Strings(names)
	return names, nil
}
