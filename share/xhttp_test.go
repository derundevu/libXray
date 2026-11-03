package share_test

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/xtls/libxray/share"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	"google.golang.org/protobuf/proto"
)

func buildXHTTP(t *testing.T, config *conf.Config) *splithttp.Config {
	t.Helper()
	if len(config.OutboundConfigs) != 1 || config.OutboundConfigs[0].StreamSetting == nil || config.OutboundConfigs[0].StreamSetting.XHTTPSettings == nil {
		t.Fatal("expected one XHTTP outbound")
	}
	message, err := config.OutboundConfigs[0].StreamSetting.XHTTPSettings.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the new generated protobuf field, not only the JSON adapter.
	encoded, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded splithttp.Config
	if err := proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return &decoded
}

func requireH2Params(t *testing.T, config *splithttp.Config, expected string) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(config.H2Params, &got); err != nil {
		t.Fatalf("invalid h2Params after protobuf round trip: %v", err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("h2Params = %#v, want %#v", got, want)
	}
}

func TestXHTTPH2ParamsLegacyExtraRoundTrip(t *testing.T) {
	const h2Params = `{"MaxConcurrentStreams":37,"MaxReadFrameSize":65536}`
	for _, network := range []string{"xhttp", "splithttp"} {
		for _, tc := range []struct {
			name      string
			fields    string
			placement string
			key       string
		}{
			{name: "legacy names", fields: `"sessionPlacement":"cookie","sessionKey":"legacy-session"`, placement: "cookie", key: "legacy-session"},
			{name: "current names take precedence", fields: `"sessionPlacement":"cookie","sessionKey":"legacy-session","sessionIDPlacement":"header","sessionIDKey":"current-session"`, placement: "header", key: "current-session"},
		} {
			t.Run(network+"/"+tc.name, func(t *testing.T) {
				extra := `{` + tc.fields + `,"h2Params":` + h2Params + `,"uplinkHTTPMethod":"OPTIONS","providerExtension":{"keep":true}}`
				query := url.Values{"type": {network}, "mode": {"packet-up"}, "extra": {extra}, "encryption": {"none"}}
				link := "vless://e9bff11a-27a7-49c7-8c21-2303c304de78@xhttp.invalid:443?" + query.Encode()
				config, err := share.ConvertShareLinksToXrayJson(link)
				if err != nil {
					t.Fatal(err)
				}
				built := buildXHTTP(t, config)
				requireH2Params(t, built, h2Params)
				if built.SessionIDPlacement != tc.placement || built.SessionIDKey != tc.key || built.UplinkHTTPMethod != "OPTIONS" {
					t.Fatalf("adapter changed effective XHTTP options: %v", built)
				}
				encoded, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				exported, err := share.ConvertXrayJsonToShareLinks(encoded)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := url.Parse(exported)
				if err != nil {
					t.Fatal(err)
				}
				var exportedExtra map[string]json.RawMessage
				if err := json.Unmarshal([]byte(parsed.Query().Get("extra")), &exportedExtra); err != nil {
					t.Fatal(err)
				}
				for _, old := range []string{"sessionPlacement", "sessionKey"} {
					if _, exists := exportedExtra[old]; exists {
						t.Fatalf("export retained obsolete %s", old)
					}
				}
				if string(exportedExtra["providerExtension"]) != `{"keep":true}` {
					t.Fatal("export lost an unknown provider option")
				}
				roundTrip, err := share.ConvertShareLinksToXrayJson(exported)
				if err != nil {
					t.Fatal(err)
				}
				if got := buildXHTTP(t, roundTrip); !proto.Equal(got, built) {
					t.Fatalf("share round trip changed effective XHTTP options: got %v, want %v", got, built)
				}
			})
		}
	}
}

func TestXHTTPH2ParamsTopLevelExport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		expected string
	}{
		{name: "top-level h2Params", settings: `{"h2Params":{"MaxConcurrentStreams":37}}`, expected: `{"MaxConcurrentStreams":37}`},
		{name: "extra h2Params take precedence", settings: `{"h2Params":{"MaxConcurrentStreams":37},"extra":{"h2Params":{"MaxConcurrentStreams":73}}}`, expected: `{"MaxConcurrentStreams":73}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := `{"outbounds":[{"protocol":"vless","settings":{"address":"xhttp.invalid","port":443,"id":"e9bff11a-27a7-49c7-8c21-2303c304de78","encryption":"none"},"streamSettings":{"network":"xhttp","xhttpSettings":` + tc.settings + `}}]}`
			exported, err := share.ConvertXrayJsonToShareLinks([]byte(config))
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := share.ConvertShareLinksToXrayJson(exported)
			if err != nil {
				t.Fatal(err)
			}
			requireH2Params(t, buildXHTTP(t, roundTrip), tc.expected)
		})
	}
}
