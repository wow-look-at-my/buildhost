module github.com/wow-look-at-my/buildhost

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/ProtonMail/go-crypto v1.4.1
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/klauspost/compress v1.18.6
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.11.1
	github.com/ulikunitz/xz v0.5.15
	github.com/wow-look-at-my/go-regex-compiler v0.0.0-20260520105527-317d2038d915
	github.com/wow-look-at-my/router v0.0.0-20260601062923-6b6e72b98f04
	go.opentelemetry.io/contrib/bridges/otelslog v0.18.0
	go.opentelemetry.io/otel v1.43.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.19.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.43.0
	go.opentelemetry.io/otel/sdk v1.43.0
	go.opentelemetry.io/otel/sdk/log v0.19.0
	go.opentelemetry.io/otel/trace v1.43.0
	modernc.org/sqlite v1.50.1
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.43.0 // indirect
	go.opentelemetry.io/otel/log v0.19.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260523011958-0a33c5d7ca68 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260523011958-0a33c5d7ca68 // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.72.5 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace gonum.org/v1/gonum v0.17.0 => github.com/gonum/gonum v0.17.0

replace go.yaml.in/yaml/v3 v3.0.4 => github.com/yaml/go-yaml/v3 v3.0.4

replace modernc.org/cc/v4 v4.28.2 => gitlab.com/cznic/cc/v4 v4.28.2

replace modernc.org/ccgo/v4 v4.34.0 => gitlab.com/cznic/ccgo/v4 v4.34.0

replace modernc.org/fileutil v1.4.0 => gitlab.com/cznic/fileutil v1.4.0

replace modernc.org/gc/v2 v2.6.5 => gitlab.com/cznic/gc/v2 v2.6.5

replace modernc.org/gc/v3 v3.1.2 => gitlab.com/cznic/gc/v3 v3.1.2

replace modernc.org/goabi0 v0.2.0 => gitlab.com/cznic/goabi0 v0.2.0

replace modernc.org/libc v1.72.5 => gitlab.com/cznic/libc v1.72.5

replace modernc.org/mathutil v1.7.1 => gitlab.com/cznic/mathutil v1.7.1

replace modernc.org/memory v1.11.0 => gitlab.com/cznic/memory v1.11.0

replace modernc.org/opt v0.2.0 => gitlab.com/cznic/opt v0.2.0

replace modernc.org/sortutil v1.2.1 => gitlab.com/cznic/sortutil v1.2.1

replace modernc.org/sqlite v1.50.1 => gitlab.com/cznic/sqlite v1.50.1

replace modernc.org/strutil v1.2.1 => gitlab.com/cznic/strutil v1.2.1

replace modernc.org/token v1.1.0 => gitlab.com/cznic/token v1.1.0
