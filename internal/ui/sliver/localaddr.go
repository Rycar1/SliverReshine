package sliver

import "net"

// LocalCallbackHost returns the address this host is reachable at from another
// machine, or "" when none can be determined.
//
// It exists for the case no other source can answer: a listener bound to
// 0.0.0.0 -- which is what the console offers by default -- reports only the
// wildcard, and an operator who reaches the console over loopback (the usual
// case, because the console listens on the same host) has no Host header that
// names a routable address either. Without this the one-liner panel can only ask
// the operator to type an address by hand, and a wildcard typed into that field
// is exactly what produced a payload that dialled 0.0.0.0 and never checked in.
//
// The address is picked the way the operating system routes outbound traffic: a
// UDP socket connected to a public address reports the local address of the
// interface the kernel would use, without sending a packet. That is the address
// a target on the same network will reach. When that yields nothing (no route),
// the first global-unicast IPv4 on a non-loopback interface is used instead,
// preferring RFC1918 ranges so a container or hypervisor bridge does not win
// over the LAN address.
//
// The result goes through the same allowlist as operator input, and a wildcard
// or loopback address is never returned: those are the values this function
// exists to avoid.
// localCallbackHost is the seam the one-liner resolver falls back to when
// nothing else names an address.
//
// It is a variable rather than a direct call so tests can pin the answer
// instead of depending on the interfaces of whatever machine runs them: the
// production behaviour is the function below, and the derivation tests that
// assert "no usable address" must not start passing or failing because the test
// runner happens to have a LAN address.
var localCallbackHost = LocalCallbackHost

func LocalCallbackHost() string {
	if ip := outboundIPv4(); ip != "" {
		if h := usableHost(ip); h != "" {
			return h
		}
	}
	for _, ip := range interfaceIPv4s() {
		if h := usableHost(ip); h != "" {
			return h
		}
	}
	return ""
}

// outboundIPv4 reports the local address of the interface the default route
// uses, or "".
//
// net.Dial on a UDP socket performs no I/O: it only asks the kernel which source
// address it would use, so this works with no network reachability and sends
// nothing.
func outboundIPv4() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	ua, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || ua.IP == nil {
		return ""
	}
	v4 := ua.IP.To4()
	if v4 == nil {
		return ""
	}
	return v4.String()
}

// interfaceIPv4s lists global-unicast IPv4 addresses on non-loopback
// interfaces, private ranges first.
func interfaceIPv4s() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var private, other []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil {
			continue
		}
		// Loopback, link-local (169.254/16), multicast and the unspecified
		// address are all things a target cannot route to.
		if ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() ||
			ipnet.IP.IsLinkLocalMulticast() || ipnet.IP.IsMulticast() ||
			ipnet.IP.IsUnspecified() {
			continue
		}
		if isPrivateIPv4(v4) {
			private = append(private, v4.String())
		} else {
			other = append(other, v4.String())
		}
	}
	return append(private, other...)
}

// isPrivateIPv4 reports whether the address is in an RFC1918 range.
func isPrivateIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	switch {
	case v4[0] == 10:
		return true
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return true
	case v4[0] == 192 && v4[1] == 168:
		return true
	}
	return false
}
