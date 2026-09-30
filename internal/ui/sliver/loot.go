package sliver

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// LootView is the JSON shape of a loot entry.
type LootView struct {
	ID           string `json:"ID"`
	Name         string `json:"Name"`
	LootType     string `json:"LootType"`
	FileType     string `json:"FileType"`
	File         string `json:"File"`
	Size         int64  `json:"Size"`
	DataB64      string `json:"DataB64,omitempty"`
	CredUser     string `json:"CredUser,omitempty"`
	CredPassword string `json:"CredPassword,omitempty"`
	CredAPIKey   string `json:"CredAPIKey,omitempty"`
}

func lootToView(l *clientpb.Loot, withData bool) *LootView {
	if l == nil {
		return nil
	}
	v := &LootView{
		ID:       l.ID,
		Name:     l.Name,
		LootType: "LOOT_FILE",
		FileType: l.FileType.String(),
	}
	if l.File != nil {
		v.File = l.File.Name
		v.Size = l.Size
		if v.Size == 0 {
			v.Size = int64(len(l.File.Data))
		}
	}
	if withData && l.File != nil {
		v.DataB64 = encodeBase64(l.File.Data)
	}
	return v
}

// credToView maps a credential to a LootView. API keys are stored with the
// reserved username "apikey" (see LootAdd); everything else is user/password.
func credToView(cred *clientpb.Credential) *LootView {
	if cred == nil {
		return nil
	}
	v := &LootView{
		ID:       cred.ID,
		Name:     cred.Collection,
		LootType: "LOOT_CREDENTIAL",
		CredUser: cred.Username,
	}
	if cred.Username == "apikey" {
		v.CredAPIKey = cred.Plaintext
	} else {
		v.CredPassword = cred.Plaintext
	}
	return v
}

// LootAll lists all loot (files and credentials) stored on the server.
func (c *Client) LootAll() ([]LootView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out := make([]LootView, 0)

	resp, err := c.RPC.LootAll(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out = append(out, lootListToViews(resp.GetLoot(), false)...)

	creds, err := c.RPC.Creds(ctx, &commonpb.Empty{})
	if err != nil {
		return out, err
	}
	for _, cred := range creds.GetCredentials() {
		if v := credToView(cred); v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

// LootAllOf lists loot filtered by type ("file" or "credential").
func (c *Client) LootAllOf(kind string) ([]LootView, error) {
	all, err := c.LootAll()
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(kind, "credential") {
		return filterLoot(all, "LOOT_CREDENTIAL"), nil
	}
	return filterLoot(all, "LOOT_FILE"), nil
}

func filterLoot(items []LootView, lootType string) []LootView {
	out := make([]LootView, 0, len(items))
	for _, l := range items {
		if l.LootType == lootType {
			out = append(out, l)
		}
	}
	return out
}

func lootListToViews(items []*clientpb.Loot, withData bool) []LootView {
	out := make([]LootView, 0, len(items))
	for _, l := range items {
		if l == nil {
			continue
		}
		if v := lootToView(l, withData); v != nil {
			out = append(out, *v)
		}
	}
	return out
}

// LootContent fetches the full data of a loot entry.
func (c *Client) LootContent(id string) (*LootView, error) {
	if cred, err := c.findCred(id); cred != nil {
		return credToView(cred), nil
	} else if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.LootContent(ctx, &clientpb.Loot{ID: id})
	if err != nil {
		return nil, err
	}
	return lootToView(resp, true), nil
}

// LootRemove deletes a loot entry from the server.
func (c *Client) LootRemove(id string) error {
	cred, err := c.findCred(id)
	if err != nil {
		return err
	}
	if cred != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := c.RPC.CredsRm(ctx, &clientpb.Credentials{Credentials: []*clientpb.Credential{cred}})
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = c.RPC.LootRm(ctx, &clientpb.Loot{ID: id})
	return err
}

// findCred looks up a credential by ID, returning (nil, nil) when the ID does
// not match a credential (so the caller can fall back to the file-loot APIs).
func (c *Client) findCred(id string) (*clientpb.Credential, error) {
	if id == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.Creds(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	for _, cred := range resp.GetCredentials() {
		if cred != nil && cred.ID == id {
			return cred, nil
		}
	}
	return nil, nil
}

// LootAddRequest is the JSON body for adding a loot entry.
type LootAddRequest struct {
	Type         string `json:"type"`          // "file" (default) | "credential"
	Name         string `json:"name"`
	FileName     string `json:"file_name"`
	FileType     string `json:"file_type"` // "text" | "binary"
	FileDataB64  string `json:"file_data_b64"`
	CredUser     string `json:"cred_user"`
	CredPassword string `json:"cred_password"`
	CredAPIKey   string `json:"cred_api_key"`
}

// LootAdd stores a new loot entry (file or credential) on the server.
func (c *Client) LootAdd(req *LootAddRequest) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch strings.ToLower(req.Type) {
	case "credential":
		cred := &clientpb.Credential{
			Collection: req.Name,
		}
		// The Sliver Credential model has no field distinguishing an API key from
		// a password, so the app uses the reserved username "apikey" as the marker
		// (see credToView). Guard the write path so a user/password credential can
		// never silently collide with that marker.
		if req.CredAPIKey != "" {
			cred.Username = "apikey"
			cred.Plaintext = req.CredAPIKey
		} else {
			if req.CredUser == "apikey" {
				return "", fmt.Errorf(`username "apikey" is reserved for API keys; use the API key type or a different username`)
			}
			cred.Username = req.CredUser
			cred.Plaintext = req.CredPassword
		}
		_, err := c.RPC.CredsAdd(ctx, &clientpb.Credentials{Credentials: []*clientpb.Credential{cred}})
		return "", err
	default:
		data, err := base64.StdEncoding.DecodeString(req.FileDataB64)
		if err != nil {
			return "", fmt.Errorf("invalid file data: %w", err)
		}
		fileType := clientpb.FileType_BINARY
		if strings.EqualFold(req.FileType, "text") {
			fileType = clientpb.FileType_TEXT
		}
		loot := &clientpb.Loot{
			Name:     req.Name,
			FileType: fileType,
			File:     &commonpb.File{Name: req.FileName, Data: data},
		}
		resp, err := c.RPC.LootAdd(ctx, loot)
		if err != nil {
			return "", err
		}
		return resp.GetID(), nil
	}
}

// LootRename updates the name of a loot entry (the only mutable field).
func (c *Client) LootRename(id, name string) error {
	if cred, err := c.findCred(id); cred != nil {
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cred.Collection = name
		_, err := c.RPC.CredsUpdate(ctx, &clientpb.Credentials{Credentials: []*clientpb.Credential{cred}})
		return err
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := c.RPC.LootUpdate(ctx, &clientpb.Loot{ID: id, Name: name})
	return err
}
