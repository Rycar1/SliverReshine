package rpc

/*
	Sliver Implant Framework
	Copyright (C) 2019  Bishop Fox

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU General Public License for more details.

	You should have received a copy of the GNU General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

import (
	"context"
	"errors"
	"fmt"

	"github.com/bishopfox/sliver/client/constants"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/server/c2"
	"github.com/bishopfox/sliver/server/core"
	"github.com/bishopfox/sliver/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultMTLSPort    = 4444
	defaultWGPort      = 53
	defaultWGNPort     = 8888
	defaultWGKeyExPort = 1337
	defaultDNSPort     = 53
	defaultHTTPPort    = 80
	defaultHTTPSPort   = 443
)

var (
	// ErrInvalidPort - Invalid TCP port number
	ErrInvalidPort = status.Error(codes.InvalidArgument, "invalid listener port")
)

// GetJobs - List jobs
func (rpc *Server) GetJobs(ctx context.Context, _ *commonpb.Empty) (*clientpb.Jobs, error) {
	jobs := &clientpb.Jobs{
		Active: []*clientpb.Job{},
	}
	for _, job := range core.Jobs.All() {
		jobs.Active = append(jobs.Active, &clientpb.Job{
			ID:          uint32(job.ID),
			Name:        job.Name,
			Description: job.Description,
			Protocol:    job.Protocol,
			Port:        uint32(job.Port),
			Domains:     job.Domains,
			ProfileName: job.ProfileName,
		})
	}
	return jobs, nil
}

// Restart Jobs - Reload jobs
func (rpc *Server) RestartJobs(ctx context.Context, restartJobReq *clientpb.RestartJobReq) (*commonpb.Empty, error) {
	// reload jobs to include new profile
	for _, jobID := range restartJobReq.JobIDs {
		job := core.Jobs.Get(int(jobID))
		listenerJob, err := db.ListenerByJobID(jobID)
		if err != nil {
			return &commonpb.Empty{}, rpcError(err)
		}
		job.JobCtrl <- true
		job, err = c2.StartHTTPListenerJob(listenerJob.HTTPConf)
		if err != nil {
			return &commonpb.Empty{}, rpcError(err)
		}
		if err := db.UpdateListenerJobID(jobID, uint32(job.ID)); err != nil {
			return &commonpb.Empty{}, rpcError(err)
		}
	}
	return &commonpb.Empty{}, nil
}

// KillJob - Kill a server-side job
func (rpc *Server) KillJob(ctx context.Context, kill *clientpb.KillJobReq) (*clientpb.KillJob, error) {
	job := core.Jobs.Get(int(kill.ID))
	killJob := &clientpb.KillJob{}
	var err error = nil
	if job != nil {
		job.JobCtrl <- true
		killJob.ID = uint32(job.ID)
		killJob.Success = true
		err = db.DeleteListener(killJob.ID)
		// A job with no persisted listener row is not a failure. Forward (bind)
		// dialers are deliberately never written to the listener table — they hold
		// no socket and must not be restored on server restart — so the delete
		// reports "record not found" for them. The job was stopped either way, and
		// surfacing an error here would tell the operator that a stop which
		// actually worked had failed.
		if err != nil && !errors.Is(err, db.ErrRecordNotFound) {
			return nil, rpcError(err)
		}
		// Clearing err is the actual fix, not the guard above. This function ends
		// with `return killJob, err`, so leaving the record-not-found error in
		// place still hands the caller an error for a stop that worked — which is
		// exactly what it did: the guard suppressed the early return, and the tail
		// returned the same error anyway.
		err = nil
	} else {
		killJob.Success = false
		err = status.Error(codes.NotFound, "invalid job ID")
	}
	return killJob, err
}

// DialBind - Start a forward (bind) listener
//
// Every other listener in this file waits for an implant to connect out. A bind
// listener is the opposite: the C2 dials a port the implant is listening on,
// which is what makes a session possible when the target cannot reach the
// internet but the operator has some path to a port on it.
//
// The call returns as soon as the dialer is running, not when a session appears.
// The implant may not have reached its listen path yet, and the dialer keeps
// retrying until it does; blocking here would make the console look hung for an
// unbounded time. The session appears in the session list when it lands.
//
// Unlike the reverse listeners this is deliberately not written to the C2
// listener table. A saved listener is restored at startup and is expected to
// bind a local port; a bind dialer holds no socket, and restoring one would
// silently start dialling a target on every server restart.
func (rpc *Server) DialBind(ctx context.Context, req *clientpb.DialBindReq) (*clientpb.DialBind, error) {
	if req.Host == "" {
		return nil, status.Error(codes.InvalidArgument, "host is required")
	}
	if req.Port == 0 || 65535 < req.Port {
		return nil, ErrInvalidPort
	}

	job, err := c2.StartBindListenerJob(req)
	if err != nil {
		return nil, rpcError(err)
	}

	return &clientpb.DialBind{
		JobID:    uint32(job.ID),
		Address:  fmt.Sprintf("%s:%d", req.Host, req.Port),
		Response: &commonpb.Response{},
	}, nil
}

// StartMTLSListener - Start an MTLS listener
func (rpc *Server) StartMTLSListener(ctx context.Context, req *clientpb.MTLSListenerReq) (*clientpb.ListenerJob, error) {
	if 65535 <= req.Port {
		return nil, ErrInvalidPort
	}
	if req.Port == 0 {
		req.Port = defaultMTLSPort
	}

	err := PortInUse(req.Port)
	if err != nil {
		return nil, rpcError(err)
	}

	job, err := c2.StartMTLSListenerJob(req)
	if err != nil {
		return nil, rpcError(err)
	}

	listenerJob := &clientpb.ListenerJob{
		JobID:    uint32(job.ID),
		Type:     constants.MtlsStr,
		MTLSConf: req,
	}
	err = db.SaveC2Listener(listenerJob)
	if err != nil {
		return nil, rpcError(err)
	}

	return &clientpb.ListenerJob{JobID: uint32(job.ID)}, nil
}

// StartWGListener - Start a Wireguard listener
func (rpc *Server) StartWGListener(ctx context.Context, req *clientpb.WGListenerReq) (*clientpb.ListenerJob, error) {

	if 65535 <= req.Port || 65535 <= req.NPort || 65535 <= req.KeyPort {
		return nil, ErrInvalidPort
	}
	if req.Port == 0 {
		req.Port = defaultWGPort
	}

	if req.NPort == 0 {
		req.NPort = defaultWGNPort
	}

	if req.KeyPort == 0 {
		req.KeyPort = defaultWGKeyExPort
	}

	if req.NPort == req.KeyPort {
		return nil, status.Error(codes.InvalidArgument, "wg nport and key-port must be different")
	}

	err := PortInUse(req.Port)
	if err != nil {
		return nil, rpcError(err)
	}

	job, err := c2.StartWGListenerJob(req)
	if err != nil {
		return nil, rpcError(err)
	}

	listenerJob := &clientpb.ListenerJob{
		JobID:  uint32(job.ID),
		Type:   constants.WGStr,
		WGConf: req,
	}
	err = db.SaveC2Listener(listenerJob)
	if err != nil {
		return nil, rpcError(err)
	}

	return &clientpb.ListenerJob{JobID: uint32(job.ID)}, nil
}

// StartDNSListener - Start a DNS listener TODO: respect request's Host specification
func (rpc *Server) StartDNSListener(ctx context.Context, req *clientpb.DNSListenerReq) (*clientpb.ListenerJob, error) {
	err := PortInUse(req.Port)
	if err != nil {
		return nil, rpcError(err)
	}

	job, err := c2.StartDNSListenerJob(req)
	if err != nil {
		return nil, rpcError(err)
	}

	listenerJob := &clientpb.ListenerJob{
		JobID:   uint32(job.ID),
		Type:    constants.DnsStr,
		DNSConf: req,
	}
	err = db.SaveC2Listener(listenerJob)
	if err != nil {
		return nil, rpcError(err)
	}

	return &clientpb.ListenerJob{JobID: uint32(job.ID)}, nil
}

// StartHTTPSListener - Start an HTTPS listener
func (rpc *Server) StartHTTPSListener(ctx context.Context, req *clientpb.HTTPListenerReq) (*clientpb.ListenerJob, error) {
	if 65535 <= req.Port {
		return nil, ErrInvalidPort
	}
	if req.Port == 0 {
		req.Port = defaultHTTPSPort
	}

	err := PortInUse(req.Port)
	if err != nil {
		return nil, rpcError(err)
	}

	job, err := c2.StartHTTPListenerJob(req)
	if err != nil {
		return nil, rpcError(err)
	}

	listenerJob := &clientpb.ListenerJob{
		JobID:    uint32(job.ID),
		Type:     constants.HttpsStr,
		HTTPConf: req,
	}
	err = db.SaveC2Listener(listenerJob)
	if err != nil {
		return nil, rpcError(err)
	}

	return &clientpb.ListenerJob{JobID: uint32(job.ID)}, nil
}

// StartHTTPListener - Start an HTTP listener
func (rpc *Server) StartHTTPListener(ctx context.Context, req *clientpb.HTTPListenerReq) (*clientpb.ListenerJob, error) {
	if 65535 <= req.Port {
		return nil, ErrInvalidPort
	}
	if req.Port == 0 {
		req.Port = defaultHTTPPort
	}

	err := PortInUse(req.Port)
	if err != nil {
		return nil, rpcError(err)
	}

	job, err := c2.StartHTTPListenerJob(req)
	if err != nil {
		return nil, rpcError(err)
	}

	listenerJob := &clientpb.ListenerJob{
		JobID:    uint32(job.ID),
		Type:     constants.HttpStr,
		HTTPConf: req,
	}
	err = db.SaveC2Listener(listenerJob)
	if err != nil {
		return nil, rpcError(err)
	}

	return &clientpb.ListenerJob{JobID: uint32(job.ID)}, nil
}

func PortInUse(newPort uint32) error {
	listenerJobs, err := db.ListenerJobs()
	if err != nil {
		return rpcError(err)
	}
	var port uint32
	for _, job := range listenerJobs {
		listener, err := db.ListenerByJobID(job.JobID)
		if err != nil {
			return rpcError(err)
		}
		switch job.Type {
		case "http":
			port = listener.HTTPConf.Port
		case "https":
			port = listener.HTTPConf.Port
		case "mtls":
			port = listener.MTLSConf.Port
		case "dns":
			port = listener.DNSConf.Port
		case "wg":
			port = listener.WGConf.Port
		case "multiplayer":
			port = listener.MultiConf.Port
		}

		if port == newPort {
			return status.Error(codes.AlreadyExists, fmt.Sprintf("port %d is in use", port))
		}
	}
	return nil
}
