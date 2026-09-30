package sliver

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// WebContentView is the JSON shape of a website content entry.
type WebContentView struct {
	Path        string `json:"Path"`
	ContentType string `json:"ContentType"`
	Size        uint64 `json:"Size"`
	DataB64     string `json:"DataB64,omitempty"`
}

// WebsiteView is the JSON shape of a hosted website.
type WebsiteView struct {
	Name     string                    `json:"Name"`
	Contents map[string]WebContentView `json:"Contents"`
	Size     uint64                    `json:"Size"`
}

func webContentToView(c *clientpb.WebContent, withData bool) WebContentView {
	v := WebContentView{}
	if c == nil {
		return v
	}
	v.Path = c.Path
	v.ContentType = c.ContentType
	v.Size = c.Size
	if withData {
		v.DataB64 = encodeBase64(c.Content)
	}
	return v
}

func websiteToView(w *clientpb.Website, withData bool) *WebsiteView {
	if w == nil {
		return nil
	}
	v := &WebsiteView{
		Name:     w.Name,
		Contents: map[string]WebContentView{},
	}
	for path, content := range w.Contents {
		cv := webContentToView(content, withData)
		v.Contents[path] = cv
		v.Size += cv.Size
	}
	return v
}

// Websites lists all websites hosted on the server.
func (c *Client) Websites() ([]WebsiteView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.RPC.Websites(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]WebsiteView, 0, len(resp.Websites))
	for _, w := range resp.Websites {
		if v := websiteToView(w, false); v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

// Website fetches a single website by name (including content data).
func (c *Client) Website(name string) (*WebsiteView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.RPC.Website(ctx, &clientpb.Website{Name: name})
	if err != nil {
		return nil, err
	}
	return websiteToView(resp, true), nil
}

// WebsiteContentRequest is the JSON body for adding or updating website content.
type WebsiteContentRequest struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	FileDataB64 string `json:"file_data_b64"`
	Text        string `json:"text"`
}

// WebsiteAddContent adds (or updates) a content entry on a website, creating
// the website if it does not exist yet.
func (c *Client) WebsiteAddContent(name string, req *WebsiteContentRequest) (*WebsiteView, error) {
	data, err := decodeContentData(req)
	if err != nil {
		return nil, err
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WebsiteAddContent(ctx, &clientpb.WebsiteAddContent{
		Name: name,
		Contents: map[string]*clientpb.WebContent{
			req.Path: {Path: req.Path, ContentType: contentType, Content: data},
		},
	})
	if err != nil {
		return nil, err
	}
	return websiteToView(resp, true), nil
}

// WebsiteUpdateContent updates the content of an existing content entry.
func (c *Client) WebsiteUpdateContent(name string, req *WebsiteContentRequest) (*WebsiteView, error) {
	data, err := decodeContentData(req)
	if err != nil {
		return nil, err
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WebsiteUpdateContent(ctx, &clientpb.WebsiteAddContent{
		Name: name,
		Contents: map[string]*clientpb.WebContent{
			req.Path: {Path: req.Path, ContentType: contentType, Content: data},
		},
	})
	if err != nil {
		return nil, err
	}
	return websiteToView(resp, true), nil
}

// WebsiteRemoveContent removes one or more content entries from a website.
func (c *Client) WebsiteRemoveContent(name string, paths []string) (*WebsiteView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WebsiteRemoveContent(ctx, &clientpb.WebsiteRemoveContent{Name: name, Paths: paths})
	if err != nil {
		return nil, err
	}
	return websiteToView(resp, true), nil
}

// WebsiteRemove deletes a website entirely.
func (c *Client) WebsiteRemove(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.RPC.WebsiteRemove(ctx, &clientpb.Website{Name: name})
	return err
}

func decodeContentData(req *WebsiteContentRequest) ([]byte, error) {
	if req.FileDataB64 != "" {
		return base64.StdEncoding.DecodeString(req.FileDataB64)
	}
	return []byte(req.Text), nil
}
