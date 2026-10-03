module sliverreshine

go 1.25.6

require (
	github.com/bishopfox/sliver v1.7.3
	golang.org/x/net v0.48.0
	golang.org/x/text v0.32.0
	google.golang.org/grpc v1.77.0
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/sys v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
)

// The console speaks the same SliverRPC surface as the server it embeds.
//
// Pointing this at the local tree is what keeps the two in step. The embedded
// server is built from _refs/sliver-173, which carries local patches (including
// the forward/bind listener); resolving the protobuf stubs from the published
// module instead would compile against a v1.7.3 that has no DialBind, and any
// later protocol change would fail at runtime rather than at build time.
replace github.com/bishopfox/sliver => ./sliver
