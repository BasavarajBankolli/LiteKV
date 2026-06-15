// Package grpcserver exposes the LiteKV engine over gRPC.
//
// We hand-write the protobuf wire types here instead of running protoc,
// so the project compiles and runs with just `go mod tidy` — no extra
// toolchain required. The service definition in proto/litekv.proto documents
// the full contract; this file implements it directly.
package grpcserver

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/BasavarajBankolli/litekv/internal/engine"
)

// ---- Minimal hand-written protobuf types ----
// Field numbers match litekv.proto exactly.
// Encoding: varint (field<<3|0), length-delimited (field<<3|2)

func pbString(field int, s string) []byte  { return pbBytes(field, []byte(s)) }
func pbBool(field int, v bool) []byte {
	b := byte(0)
	if v { b = 1 }
	return append(pbTag(field, 0), b)
}
func pbBytes(field int, b []byte) []byte {
	out := pbTag(field, 2)
	out = append(out, pbVarint(uint64(len(b)))...)
	return append(out, b...)
}
func pbTag(field, wire int) []byte { return pbVarint(uint64(field<<3 | wire)) }
func pbVarint(v uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], v)
	return buf[:n]
}

func readVarint(r io.Reader) (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < 10; i++ {
		var b [1]byte
		if _, err := r.Read(b[:]); err != nil { return 0, err }
		x |= uint64(b[0]&0x7f) << s
		if b[0] < 0x80 { return x, nil }
		s += 7
	}
	return 0, fmt.Errorf("varint overflow")
}

// ---- gRPC service descriptor ----

const serviceName = "litekv.KVService"

var kvServiceDesc = grpc.ServiceDesc{
	ServiceName: serviceName,
	HandlerType: (*kvServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Get",    Handler: handleGet},
		{MethodName: "Put",    Handler: handlePut},
		{MethodName: "Delete", Handler: handleDelete},
		{MethodName: "Stats",  Handler: handleStats},
	},
	Streams: []grpc.StreamDesc{},
}

type kvServiceServer interface{}

// ---- Handlers ----

func handleGet(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	s := srv.(*KVServer)
	var req getRequest
	if err := dec(&req); err != nil { return nil, err }
	val, err := s.eng.Get(req.Key)
	if err != nil { return nil, status.Errorf(codes.Internal, "%v", err) }
	resp := &getResponse{Value: val, Found: val != nil}
	return resp, nil
}

func handlePut(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	s := srv.(*KVServer)
	var req putRequest
	if err := dec(&req); err != nil { return nil, err }
	if err := s.eng.Put(req.Key, req.Value); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &putResponse{Ok: true}, nil
}

func handleDelete(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	s := srv.(*KVServer)
	var req deleteRequest
	if err := dec(&req); err != nil { return nil, err }
	if err := s.eng.Delete(req.Key); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &deleteResponse{Ok: true}, nil
}

func handleStats(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	s := srv.(*KVServer)
	var req statsRequest
	if err := dec(&req); err != nil { return nil, err }
	stats := s.eng.Stats()
	return &statsResponse{
		MemtableSizeBytes: stats["memtable_size_bytes"].(int64),
		MemtableEntries:   int64(stats["memtable_entries"].(int)),
		WalSizeBytes:      stats["wal_size_bytes"].(int64),
		ClockVersion:      int64(stats["clock_version"].(uint64)),
	}, nil
}

// ---- Message types with codec ----

type getRequest  struct{ Key string }
type getResponse struct{ Value []byte; Found bool }
type putRequest  struct{ Key string; Value []byte }
type putResponse struct{ Ok bool }
type deleteRequest struct{ Key string }
type deleteResponse struct{ Ok bool }
type statsRequest struct{}
type statsResponse struct {
	MemtableSizeBytes int64
	MemtableEntries   int64
	WalSizeBytes      int64
	ClockVersion      int64
}

// Marshal / Unmarshal implement grpc.Codec for our hand-written types.
// Registered as the "proto" codec so gRPC clients with real protoc-generated
// stubs also work.

type kvCodec struct{}

func (kvCodec) Marshal(v interface{}) ([]byte, error) {
	switch m := v.(type) {
	case *getResponse:
		var b []byte
		if len(m.Value) > 0 { b = append(b, pbBytes(1, m.Value)...) }
		b = append(b, pbBool(2, m.Found)...)
		return b, nil
	case *putResponse:
		return pbBool(1, m.Ok), nil
	case *deleteResponse:
		return pbBool(1, m.Ok), nil
	case *statsResponse:
		var b []byte
		b = append(b, append(pbTag(1,0), pbVarint(uint64(m.MemtableSizeBytes))...)...)
		b = append(b, append(pbTag(2,0), pbVarint(uint64(m.MemtableEntries))...)...)
		b = append(b, append(pbTag(3,0), pbVarint(uint64(m.WalSizeBytes))...)...)
		b = append(b, append(pbTag(4,0), pbVarint(uint64(m.ClockVersion))...)...)
		return b, nil
	}
	return nil, fmt.Errorf("grpc: unknown message type %T", v)
}

func (kvCodec) Unmarshal(data []byte, v interface{}) error {
	switch m := v.(type) {
	case *getRequest:
		return decodeGetRequest(data, m)
	case *putRequest:
		return decodePutRequest(data, m)
	case *deleteRequest:
		return decodeDeleteRequest(data, m)
	case *statsRequest:
		return nil
	}
	return nil
}

func (kvCodec) Name() string { return "proto" }

func decodeGetRequest(data []byte, m *getRequest) error {
	for i := 0; i < len(data); {
		tag, n := decodeVarintAt(data, i); i += n
		field, wire := int(tag>>3), tag&0x7
		if wire == 2 {
			length, n2 := decodeVarintAt(data, i); i += n2
			payload := data[i:i+int(length)]; i += int(length)
			if field == 1 { m.Key = string(payload) }
		} else { i++ }
	}
	return nil
}

func decodePutRequest(data []byte, m *putRequest) error {
	for i := 0; i < len(data); {
		tag, n := decodeVarintAt(data, i); i += n
		field, wire := int(tag>>3), tag&0x7
		if wire == 2 {
			length, n2 := decodeVarintAt(data, i); i += n2
			payload := data[i:i+int(length)]; i += int(length)
			if field == 1 { m.Key = string(payload) }
			if field == 2 { m.Value = payload }
		} else { i++ }
	}
	return nil
}

func decodeDeleteRequest(data []byte, m *deleteRequest) error {
	for i := 0; i < len(data); {
		tag, n := decodeVarintAt(data, i); i += n
		field, wire := int(tag>>3), tag&0x7
		if wire == 2 {
			length, n2 := decodeVarintAt(data, i); i += n2
			payload := data[i:i+int(length)]; i += int(length)
			if field == 1 { m.Key = string(payload) }
		} else { i++ }
	}
	return nil
}

func decodeVarintAt(data []byte, i int) (uint64, int) {
	var x uint64; var s uint
	for j := i; j < len(data); j++ {
		b := data[j]
		x |= uint64(b&0x7f) << s
		if b < 0x80 { return x, j-i+1 }
		s += 7
	}
	return x, 1
}

// ---- KVServer + lifecycle ----

// KVServer is the gRPC service handler.
type KVServer struct{ eng *engine.Engine }

// Server manages the gRPC listener.
type Server struct {
	kv   *KVServer
	grpc *grpc.Server
	addr string
}

// NewServer creates a gRPC server that actually serves KV requests on addr.
func NewServer(addr string, eng *engine.Engine) *Server {
	gs := grpc.NewServer(
		grpc.ForceServerCodec(kvCodec{}),
		grpc.MaxRecvMsgSize(16*1024*1024),
	)
	kv := &KVServer{eng: eng}
	gs.RegisterService(&kvServiceDesc, kv)
	return &Server{kv: kv, grpc: gs, addr: addr}
}

// Serve starts accepting connections. Blocks until stopped.
func (s *Server) Serve() error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("grpc: listen %s: %w", s.addr, err)
	}
	fmt.Printf("[gRPC] Listening on %s\n", s.addr)
	return s.grpc.Serve(lis)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() { s.grpc.GracefulStop() }
