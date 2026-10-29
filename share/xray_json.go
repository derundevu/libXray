package share

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"
)

var errUnsupportedCapability = errors.New("unsupported capability")

func checkShadowsocksCapabilities(cipher string, udpOverTCP bool) error {
	if udpOverTCP {
		return fmt.Errorf("%w: Shadowsocks UDP-over-TCP is not supported by this Xray version", errUnsupportedCapability)
	}
	switch strings.ToLower(cipher) {
	case "none", "plain":
		return fmt.Errorf("%w: Shadowsocks cipher %q was removed by this Xray version", errUnsupportedCapability, cipher)
	}
	return nil
}

func normalizeXHTTPExtra(raw json.RawMessage) (json.RawMessage, error) {
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, err
	}
	// Xray renamed these fields; retain all other provider options verbatim.
	for _, rename := range [...][2]string{
		{"sessionPlacement", "sessionIDPlacement"},
		{"sessionKey", "sessionIDKey"},
	} {
		old, current := rename[0], rename[1]
		if value, ok := extra[old]; ok {
			if _, exists := extra[current]; !exists {
				extra[current] = value
			}
			delete(extra, old)
		}
	}
	normalized, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	var settings conf.SplitHTTPConfig
	if err := json.Unmarshal(normalized, &settings); err != nil {
		return nil, err
	}
	return normalized, nil
}

type XrayRawSettingsHeader struct {
	Type    string                        `json:"type,omitempty"`
	Request *XrayRawSettingsHeaderRequest `json:"request,omitempty"`
}

type XrayRawSettingsHeaderRequest struct {
	Path    []string                             `json:"path,omitempty"`
	Headers *XrayRawSettingsHeaderRequestHeaders `json:"headers,omitempty"`
}

type XrayRawSettingsHeaderRequestHeaders struct {
	Host []string `json:"Host,omitempty"`
}

type XrayFakeHeader struct {
	Type string `json:"type,omitempty"`
}

func setOutboundName(outbound *conf.OutboundDetourConfig, name string) {
	outbound.SendThrough = &name
}

func getOutboundName(outbound conf.OutboundDetourConfig) string {
	if outbound.SendThrough != nil {
		if len(*outbound.SendThrough) > 0 {
			return *outbound.SendThrough
		}
	}
	if len(outbound.Tag) > 0 {
		return outbound.Tag
	}
	if len(outbound.Protocol) > 0 {
		return outbound.Protocol
	}
	return ""
}

func parseAddress(addr string) *conf.Address {
	address := &conf.Address{}
	address.Address = net.ParseAddress(addr)
	return address
}

func convertJsonToRawMessage(v any) (json.RawMessage, error) {
	vBytes, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(vBytes), nil
}
