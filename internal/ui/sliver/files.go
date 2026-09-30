package sliver

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

const opTimeout = 30 * time.Second

// FileInfoView is the JSON shape for a directory entry.
type FileInfoView struct {
	Name    string `json:"Name"`
	IsDir   bool   `json:"IsDir"`
	Size    int64  `json:"Size"`
	ModTime int64  `json:"ModTime"`
	Mode    string `json:"Mode"`
}

// DirView is the JSON shape for a directory listing.
type DirView struct {
	Path  string         `json:"Path"`
	Exists bool          `json:"Exists"`
	Files []FileInfoView `json:"Files"`
}

func dirToView(d *sliverpb.Ls) *DirView {
	if d == nil {
		return nil
	}
	out := &DirView{Path: d.Path, Exists: d.Exists}
	files := make([]FileInfoView, 0, len(d.Files))
	for _, f := range d.Files {
		if f == nil {
			continue
		}
		files = append(files, FileInfoView{
			Name:    f.Name,
			IsDir:   f.IsDir,
			Size:    f.Size,
			ModTime: f.ModTime,
			Mode:    f.Mode,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		return files[i].Name < files[j].Name
	})
	out.Files = files
	return out
}

// Ls lists a directory on the session.
func (c *Client) Ls(sessionID, path string) (*DirView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.Ls(ctx, &sliverpb.LsReq{
		Path: path,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return dirToView(resp), nil
}

// Pwd returns the current working directory of the session.
func (c *Client) Pwd(sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.Pwd(ctx, &sliverpb.PwdReq{
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return "", err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return "", fmt.Errorf("%s", resp.Response.Err)
	}
	return resp.Path, nil
}

// Cd changes the working directory on the session.
func (c *Client) Cd(sessionID, path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.Cd(ctx, &sliverpb.CdReq{
		Path: path,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return "", err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return "", fmt.Errorf("%s", resp.Response.Err)
	}
	return resp.Path, nil
}

// Download reads a file from the session and returns its base64-encoded data.
func (c *Client) Download(sessionID, path string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.Download(ctx, &sliverpb.DownloadReq{
		Path: path,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return "", "", err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return "", "", fmt.Errorf("%s", resp.Response.Err)
	}
	if !resp.Exists {
		return "", "", fmt.Errorf("remote file %q does not exist", path)
	}
	return encodeBase64(resp.Data), resp.Path, nil
}

// Upload writes data to a file on the session.
func (c *Client) Upload(sessionID, path string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.Upload(ctx, &sliverpb.UploadReq{
		Path: path,
		Data: data,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// Mkdir creates a directory on the session.
func (c *Client) Mkdir(sessionID, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.Mkdir(ctx, &sliverpb.MkdirReq{
		Path: path,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// Rm removes a file or directory on the session.
func (c *Client) Rm(sessionID, path string, recursive bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.Rm(ctx, &sliverpb.RmReq{
		Path:      path,
		Recursive: recursive,
		Force:     true,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// Mv moves or renames a file on the session.
func (c *Client) Mv(sessionID, src, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.Mv(ctx, &sliverpb.MvReq{
		Src: src,
		Dst: dst,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}
