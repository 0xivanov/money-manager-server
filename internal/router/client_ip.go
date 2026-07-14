package router

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func clientIP(request *http.Request, trustedProxyCIDRs []netip.Prefix, trustedProxyHops int) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || host == "" {
		host = request.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return request.RemoteAddr
	}
	remote = remote.Unmap()
	if trustedProxyHops <= 0 || !addressInPrefixes(remote, trustedProxyCIDRs) {
		return remote.String()
	}

	forwardedValues := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	forwarded := make([]netip.Addr, 0, len(forwardedValues))
	for _, value := range forwardedValues {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			forwarded = nil
			break
		}
		forwarded = append(forwarded, address.Unmap())
	}
	clientIndex := len(forwarded) - trustedProxyHops
	if clientIndex >= 0 {
		for index := clientIndex + 1; index < len(forwarded); index++ {
			if !addressInPrefixes(forwarded[index], trustedProxyCIDRs) {
				return remote.String()
			}
		}
		return forwarded[clientIndex].String()
	}
	if trustedProxyHops == 1 {
		if realIP, err := netip.ParseAddr(strings.TrimSpace(request.Header.Get("X-Real-IP"))); err == nil {
			return realIP.Unmap().String()
		}
	}
	return remote.String()
}

func addressInPrefixes(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
