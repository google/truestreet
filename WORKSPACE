load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

http_archive(
    name = "io_bazel_rules_go",
    urls = [
        "https://storage.googleapis.com/bazel-mirror/github.com/bazelbuild/rules_go/releases/download/v0.21.0/rules_go-v0.21.0.tar.gz",
        "https://github.com/bazelbuild/rules_go/releases/download/v0.21.0/rules_go-v0.21.0.tar.gz",
    ],
    sha256 = "b27e55d2dcc9e6020e17614ae6e0374818a3e3ce6f2024036e688ada24110444",
)

load("@io_bazel_rules_go//go:deps.bzl", "go_rules_dependencies", "go_register_toolchains")

go_rules_dependencies()

go_register_toolchains()

http_archive(
    name = "bazel_gazelle",
    urls = [
        "https://storage.googleapis.com/bazel-mirror/github.com/bazelbuild/bazel-gazelle/releases/download/v0.19.1/bazel-gazelle-v0.19.1.tar.gz",
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.19.1/bazel-gazelle-v0.19.1.tar.gz",
    ],
    sha256 = "86c6d481b3f7aedc1d60c1c211c6f76da282ae197c3b3160f54bd3a8f847896f",
)

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies", "go_repository")

gazelle_dependencies()

http_archive(
    name = "rules_proto",
    sha256 = "602e7161d9195e50246177e7c55b2f39950a9cf7366f74ed5f22fd45750cd208",
    strip_prefix = "rules_proto-97d8af4dc474595af3900dd85cb3a29ad28cc313",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/rules_proto/archive/97d8af4dc474595af3900dd85cb3a29ad28cc313.tar.gz",
        "https://github.com/bazelbuild/rules_proto/archive/97d8af4dc474595af3900dd85cb3a29ad28cc313.tar.gz",
    ],
)

load("@rules_proto//proto:repositories.bzl", "rules_proto_dependencies", "rules_proto_toolchains")

rules_proto_dependencies()

rules_proto_toolchains()

http_archive(
    name = "io_bazel_rules_docker",
    sha256 = "6287241e033d247e9da5ff705dd6ef526bac39ae82f3d17de1b69f8cb313f9cd",
    strip_prefix = "rules_docker-0.14.3",
    urls = ["https://github.com/bazelbuild/rules_docker/releases/download/v0.14.3/rules_docker-v0.14.3.tar.gz"],
)

load(
    "@io_bazel_rules_docker//repositories:repositories.bzl",
    container_repositories = "repositories",
)
container_repositories()

load(
    "@io_bazel_rules_docker//go:image.bzl",
    _go_image_repos = "repositories",
)

_go_image_repos()

go_repository(
    name = "co_honnef_go_tools",
    build_file_proto_mode = "disable",
    commit = "afd67930eec2a9ed3e9b19f684d17a062285f16a",
    importpath = "honnef.co/go/tools",
)

go_repository(
    name = "com_github_beorn7_perks",
    build_file_proto_mode = "disable",
    commit = "37c8de3658fcb183f997c4e13e8337516ab753e6",
    importpath = "github.com/beorn7/perks",
)

go_repository(
    name = "com_github_burntsushi_toml",
    build_file_proto_mode = "disable",
    commit = "3012a1dbe2e4bd1391d42b32f0577cb7bbc7f005",
    importpath = "github.com/BurntSushi/toml",
)

go_repository(
    name = "com_github_gogo_protobuf",
    build_file_proto_mode = "disable",
    commit = "5628607bb4c51c3157aacc3a50f0ab707582b805",
    importpath = "github.com/gogo/protobuf",
)

go_repository(
    name = "com_github_golang_groupcache",
    build_file_proto_mode = "disable",
    commit = "8c9f03a8e57eb486e42badaed3fb287da51807ba",
    importpath = "github.com/golang/groupcache",
)

go_repository(
    name = "com_github_golang_protobuf",
    build_file_proto_mode = "disable",
    commit = "d23c5127dc24889085f8ccea5c9d560a57a879d8",
    importpath = "github.com/golang/protobuf",
)

go_repository(
    name = "com_github_golang_snappy",
    build_file_proto_mode = "disable",
    commit = "2a8bb927dd31d8daada140a5d09578521ce5c36a",
    importpath = "github.com/golang/snappy",
)

go_repository(
    name = "com_github_google_go_cmp",
    build_file_proto_mode = "disable",
    commit = "5a6f75716e1203a923a78c9efb94089d857df0f6",
    importpath = "github.com/google/go-cmp",
)

go_repository(
    name = "com_github_googleapis_gax_go",
    build_file_proto_mode = "disable",
    commit = "bd5b16380fd03dc758d11cef74ba2e3bc8b0e8c2",
    importpath = "github.com/googleapis/gax-go",
)

go_repository(
    name = "com_github_grpc_ecosystem_grpc_gateway",
    commit = "4c2cec4158f65aebe0290d3bee883b13f7b07c6f",
    build_file_proto_mode = "disable",
    importpath = "github.com/grpc-ecosystem/grpc-gateway",
)

go_repository(
    name = "com_github_jstemmer_go_junit_report",
    build_file_proto_mode = "disable",
    commit = "cc1f095d5cc5eca2844f5c5ea7bb37f6b9bf6cac",
    importpath = "github.com/jstemmer/go-junit-report",
)

go_repository(
    name = "com_github_kylelemons_godebug",
    build_file_proto_mode = "disable",
    commit = "9ff306d4fbead574800b66369df5b6144732d58e",
    importpath = "github.com/kylelemons/godebug",
)

go_repository(
    name = "com_github_matttproud_golang_protobuf_extensions",
    build_file_proto_mode = "disable",
    commit = "c12348ce28de40eed0136aa2b644d0ee0650e56c",
    importpath = "github.com/matttproud/golang_protobuf_extensions",
)

go_repository(
    name = "com_github_prometheus_client_golang",
    build_file_proto_mode = "disable",
    commit = "170205fb58decfd011f1550d4cfb737230d7ae4f",
    importpath = "github.com/prometheus/client_golang",
)

go_repository(
    name = "com_github_prometheus_client_model",
    build_file_proto_mode = "disable",
    commit = "7bc5445566f0fe75b15de23e6b93886e982d7bf9",
    importpath = "github.com/prometheus/client_model",
)

go_repository(
    name = "com_github_prometheus_common",
    build_file_proto_mode = "disable",
    commit = "d978bcb1309602d68bb4ba69cf3f8ed900e07308",
    importpath = "github.com/prometheus/common",
)

go_repository(
    name = "com_github_prometheus_procfs",
    build_file_proto_mode = "disable",
    commit = "6d489fc7f1d9cd890a250f3ea3431b1744b9623f",
    importpath = "github.com/prometheus/procfs",
)

go_repository(
    name = "com_github_prometheus_prometheus",
    build_file_proto_mode = "disable",
    commit = "d9613e5c466c6e9de548c4dae1b9aabf9aaf7c57",
    importpath = "github.com/prometheus/prometheus",
)

go_repository(
    name = "com_google_cloud_go",
    build_file_proto_mode = "disable",
    commit = "6daa679260d92196ffca2362d652c924fdcb7a22",
    importpath = "cloud.google.com/go",
)

go_repository(
    name = "io_opencensus_go",
    build_file_proto_mode = "disable",
    commit = "d835ff86be02193d324330acdb7d65546b05f814",
    importpath = "go.opencensus.io",
)

go_repository(
    name = "org_golang_google_api",
    build_file_proto_mode = "disable",
    commit = "6f5c88b9e8c709c0f1fff128c30b041c71d14da4",
    importpath = "google.golang.org/api",
)

go_repository(
    name = "org_golang_google_appengine",
    build_file_proto_mode = "disable",
    commit = "971852bfffca25b069c31162ae8f247a3dba083b",
    importpath = "google.golang.org/appengine",
)

go_repository(
    name = "org_golang_google_genproto",
    build_file_proto_mode = "disable",
    commit = "2dc5924e38981f069b4982c6eb2b72365efd00a7",
    importpath = "google.golang.org/genproto",
)

go_repository(
    name = "org_golang_google_grpc",
    build_file_proto_mode = "disable",
    commit = "f495f5b15ae7ccda3b38c53a1bfcde4c1a58a2bc",
    importpath = "google.golang.org/grpc",
)

go_repository(
    name = "org_golang_x_exp",
    build_file_proto_mode = "disable",
    commit = "f17229e696bd4e065144fd6ae1313e41515abbdc",
    importpath = "golang.org/x/exp",
)

go_repository(
    name = "org_golang_x_lint",
    build_file_proto_mode = "disable",
    commit = "910be7a94367618fd0fd25eaabbee4fdc0ac7092",
    importpath = "golang.org/x/lint",
)

go_repository(
    name = "org_golang_x_mod",
    build_file_proto_mode = "disable",
    commit = "ed3ec21bb8e252814c380df79a80f366440ddb2d",
    importpath = "golang.org/x/mod",
)

go_repository(
    name = "org_golang_x_net",
    build_file_proto_mode = "disable",
    commit = "16171245cfb220d5317888b716d69c1fb4e7992b",
    importpath = "golang.org/x/net",
)

go_repository(
    name = "org_golang_x_oauth2",
    build_file_proto_mode = "disable",
    commit = "bf48bf16ab8d622ce64ec6ce98d2c98f916b6303",
    importpath = "golang.org/x/oauth2",
)

go_repository(
    name = "org_golang_x_sys",
    build_file_proto_mode = "disable",
    commit = "d101bd2416d505c0448a6ce8a282482678040a89",
    importpath = "golang.org/x/sys",
)

go_repository(
    name = "org_golang_x_text",
    build_file_proto_mode = "disable",
    commit = "342b2e1fbaa52c93f31447ad2c6abc048c63e475",
    importpath = "golang.org/x/text",
)

go_repository(
    name = "org_golang_x_tools",
    build_file_proto_mode = "disable",
    commit = "11eff242d136374289f76e9313c76e9312391172",
    importpath = "golang.org/x/tools",
)

go_repository(
    name = "org_golang_x_xerrors",
    build_file_proto_mode = "disable",
    commit = "9bdfabe68543c54f90421aeb9a60ef8061b5b544",
    importpath = "golang.org/x/xerrors",
)
