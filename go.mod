module github.com/stripe/veneur

go 1.14

require (
	github.com/DataDog/datadog-go v3.7.2+incompatible
	github.com/Shopify/sarama v1.15.0
	github.com/araddon/dateparse v0.0.0-20180318191655-f58c961370c5
	github.com/aws/aws-sdk-go v1.10.17
	github.com/axiomhq/hyperloglog v0.0.0-20171114175703-8300947202c9
	github.com/beorn7/perks v1.0.1
	github.com/davecgh/go-spew v1.1.0
	github.com/dgryski/go-bits v0.0.0-20160601073636-2ad8d707cc05
	github.com/dgryski/go-metro v0.0.0-20170608043646-0f6473574cdf
	github.com/eapache/go-resiliency v1.0.0
	github.com/eapache/go-xerial-snappy v0.0.0-20160609142408-bb955e01b934
	github.com/eapache/queue v1.1.0
	github.com/getsentry/sentry-go v0.6.2-0.20200616133211-abb91bfdb057
	github.com/ghodss/yaml v1.0.0
	github.com/go-ini/ini v1.28.0
	github.com/gogo/protobuf v1.2.1
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/protobuf v1.2.1-0.20181128192352-1d3f30b51784
	github.com/golang/snappy v0.0.0-20170215233205-553a64147049
	github.com/google/btree v0.0.0-20180124185431-e89373fe6b4a
	github.com/google/go-querystring v1.0.0
	github.com/google/gofuzz v0.0.0-20170612174753-24818f796faf
	github.com/googleapis/gnostic v0.1.0
	github.com/gregjones/httpcache v0.0.0-20171119193500-2bcd89a1743f
	github.com/hashicorp/consul v0.9.0
	github.com/hashicorp/go-cleanhttp v0.0.0-20170211013415-3573b8b52aa7
	github.com/hashicorp/go-retryablehttp v0.6.6
	github.com/hashicorp/go-rootcerts v0.0.0-20160503143440-6bb64b370b90
	github.com/hashicorp/serf v0.8.1
	github.com/imdario/mergo v0.3.10
	github.com/jmespath/go-jmespath v0.0.0-20151117175822-3433f3ea46d9
	github.com/json-iterator/go v0.0.0-20180128090011-28452fcdec4e
	github.com/juju/ratelimit v1.0.1
	github.com/kelseyhightower/envconfig v1.3.0
	github.com/konsorten/go-windows-terminal-sequences v1.0.3
	github.com/kr/logfmt v0.0.0-20140226030751-b84e30acd515
	github.com/lightstep/lightstep-tracer-go v0.13.1-0.20170818234450-ea8cdd9df863
	github.com/mailru/easyjson v0.0.0-20180606163543-3fdea8d05856
	github.com/matttproud/golang_protobuf_extensions v1.0.0
	github.com/mitchellh/go-homedir v0.0.0-20161203194507-b8bc1bf76747
	github.com/newrelic/newrelic-client-go v0.39.0
	github.com/newrelic/newrelic-telemetry-sdk-go v0.4.0
	github.com/opentracing/opentracing-go v1.0.2
	github.com/petar/GoLLRB v0.0.0-20130427215148-53be0d36a84c
	github.com/peterbourgon/diskv v2.0.1+incompatible
	github.com/pierrec/lz4 v1.0.2-0.20171218195038-2fcda4cb7018
	github.com/pierrec/xxHash v0.1.1
	github.com/pkg/errors v0.8.0
	github.com/pkg/profile v1.2.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/prometheus/client_golang v1.1.0
	github.com/prometheus/client_model v0.0.0-20170216185247-6f3806018612
	github.com/prometheus/common v0.0.0-20170731114204-61f87aac8082
	github.com/prometheus/procfs v0.0.5
	github.com/rcrowley/go-metrics v0.0.0-20171128170426-e181e095bae9
	github.com/satori/go.uuid v1.2.1-0.20180103174451-36e9d2ebbde5
	github.com/segmentio/fasthash v1.0.0
	github.com/signalfx/com_signalfx_metrics_protobuf v0.0.0-20170330202426-93e507b42f43
	github.com/signalfx/gohistogram v0.0.0-20160107210732-1ccfd2ff5083
	github.com/signalfx/golib v2.0.0+incompatible
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/pflag v1.0.0
	github.com/stretchr/testify v1.2.1
	github.com/theckman/go-flock v0.0.0-20170522022801-6de226b0d5f0
	github.com/tomnomnom/linkheader v0.0.0-20160328204959-6953a30d4443
	github.com/zenazn/goji v0.9.1-0.20160507202103-64eb34159fe5
	goji.io v1.1.0
	golang.org/x/net v0.0.0-20181129055619-fae4c4e3ad76
	golang.org/x/sys v0.0.0-20181208175041-ad97f365e150
	golang.org/x/text v0.0.0-20170714085652-836efe42bb4a
	google.golang.org/genproto v0.0.0-20170711235230-b0a3dcfcd1a9
	google.golang.org/grpc v1.17.0
	gopkg.in/inf.v0 v0.9.0
	gopkg.in/logfmt.v0 v0.3.0
	gopkg.in/stack.v1 v1.6.0
	gopkg.in/yaml.v2 v2.0.0-20170721122051-25c4ec802a7d
	k8s.io/api v0.0.0-20180213175254-0d0b2f481328
	k8s.io/apimachinery v0.0.0-20180213174950-da3b134bab57
	k8s.io/client-go v6.0.0+incompatible
	stathat.com/c/consistent v1.0.0
)
