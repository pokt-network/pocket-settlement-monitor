# Tiltfile — Pocket Settlement Monitor dev environment
#
# Usage:
#   cp tilt_config.example.yaml tilt_config.yaml  # first time only
#   tilt up

# ---------------------------------------------------------------------------
# Config loading
# ---------------------------------------------------------------------------

config_path = "tilt_config.yaml"
if not os.path.exists(config_path):
    fail("""
tilt_config.yaml not found!

  cp tilt_config.example.yaml tilt_config.yaml

Then edit it with your RPC URL and preferences, and re-run 'tilt up'.
""")

cfg = read_yaml(config_path)

# ---------------------------------------------------------------------------
# Extract settings with defaults
# ---------------------------------------------------------------------------

monitor_cfg = cfg.get("monitor", {})
ports = cfg.get("ports", {})
obs = cfg.get("observability", {})
mock_wh = cfg.get("mock_webhook", {})

port_monitor = ports.get("monitor_metrics", 9090)
port_prometheus = ports.get("prometheus", 9091)
port_grafana = ports.get("grafana", 3000)
port_loki = ports.get("loki", 3100)
port_mock_webhook = ports.get("mock_webhook", 8888)

prometheus_enabled = obs.get("prometheus", {}).get("enabled", True)
grafana_enabled = obs.get("grafana", {}).get("enabled", True)
loki_enabled = obs.get("loki", {}).get("enabled", False)
mock_webhook_enabled = mock_wh.get("enabled", False)

# ---------------------------------------------------------------------------
# Generate monitor config with Docker overrides
# ---------------------------------------------------------------------------

gen_cfg = dict(monitor_cfg)

# Override database path to use container volume, preserving the configured filename.
db = dict(gen_cfg.get("database", {}))
db_filename = db.get("path", "settlement-monitor.db").split("/")[-1]
db["path"] = "/home/psm/" + db_filename
gen_cfg["database"] = db

# Override metrics addr to bind all interfaces in container (fixed internal port)
met = dict(gen_cfg.get("metrics", {}))
met["addr"] = "0.0.0.0:9090"
gen_cfg["metrics"] = met

# Force JSON logging when Loki is enabled
if loki_enabled:
    log = dict(gen_cfg.get("logging", {}))
    log["format"] = "json"
    gen_cfg["logging"] = log

# Point webhook URLs to mock service when enabled
if mock_webhook_enabled:
    notif = dict(gen_cfg.get("notifications", {}))
    mock_base = "http://mock-webhook:8888"
    if not notif.get("webhook_url"):
        notif["webhook_url"] = mock_base + "/webhook"
    if not notif.get("critical_webhook_url"):
        notif["critical_webhook_url"] = mock_base + "/webhook-critical"
    if not notif.get("ops_webhook_url"):
        notif["ops_webhook_url"] = mock_base + "/webhook-ops"
    gen_cfg["notifications"] = notif

# Write generated config
local("mkdir -p .tilt")

# Use local() to write the YAML via a Python-ish approach:
# We'll serialize the config to YAML using a shell command.
gen_yaml_lines = []

def yaml_val(v):
    # Simple YAML value serializer for scalars.
    if type(v) == "bool":
        return "true" if v else "false"
    if type(v) == "int":
        return str(v)
    if type(v) == "float":
        return str(v)
    if type(v) == "string":
        if v == "":
            return '""'
        # Quote strings that might be ambiguous
        for ch in [":", "{", "}", "[", "]", ",", "&", "*", "?", "|", "-", "<", ">", "=", "!", "%", "@", "`"]:
            if ch in v:
                return '"%s"' % v
        if v in ["true", "false", "yes", "no", "null"]:
            return '"%s"' % v
        return '"%s"' % v
    return str(v)

def to_yaml(obj, indent=0):
    # Convert a dict/list/scalar to YAML lines.
    lines = []
    prefix = "  " * indent
    if type(obj) == "dict":
        for k in obj:
            v = obj[k]
            if type(v) == "dict":
                lines.append("%s%s:" % (prefix, k))
                lines += to_yaml(v, indent + 1)
            elif type(v) == "list":
                lines.append("%s%s:" % (prefix, k))
                if len(v) == 0:
                    # Rewrite as empty list on same line
                    lines[-1] = "%s%s: []" % (prefix, k)
                else:
                    for item in v:
                        if type(item) == "dict":
                            first = True
                            for ik in item:
                                if first:
                                    lines.append("%s  - %s: %s" % (prefix, ik, yaml_val(item[ik])))
                                    first = False
                                else:
                                    lines.append("%s    %s: %s" % (prefix, ik, yaml_val(item[ik])))
                        else:
                            lines.append("%s  - %s" % (prefix, yaml_val(item)))
            else:
                lines.append("%s%s: %s" % (prefix, k, yaml_val(v)))
    return lines

generated_yaml = "\n".join(to_yaml(gen_cfg)) + "\n"

local("cat > .tilt/.generated-config.yaml << 'TILT_EOF'\n%s\nTILT_EOF" % generated_yaml)

# ---------------------------------------------------------------------------
# Compose profiles
# ---------------------------------------------------------------------------

profiles = []
if prometheus_enabled or grafana_enabled:
    profiles.append("observability")
if loki_enabled:
    profiles.append("loki")
if mock_webhook_enabled:
    profiles.append("mock-webhook")

os.environ["COMPOSE_PROFILES"] = ",".join(profiles) if profiles else ""
os.environ["MONITOR_METRICS_PORT"] = str(port_monitor)
os.environ["PROMETHEUS_PORT"] = str(port_prometheus)
os.environ["GRAFANA_PORT"] = str(port_grafana)
os.environ["LOKI_PORT"] = str(port_loki)
os.environ["MOCK_WEBHOOK_PORT"] = str(port_mock_webhook)

# ---------------------------------------------------------------------------
# Docker builds
# ---------------------------------------------------------------------------

docker_build(
    "pocket-settlement-monitor:local",
    context=".",
    dockerfile="Dockerfile",
    only=[
        "go.mod", "go.sum",
        "main.go", "Makefile",
        "cmd/", "config/", "subscriber/", "processor/",
        "store/", "notify/", "metrics/", "logging/", "internal/",
    ],
)

if mock_webhook_enabled:
    docker_build(
        "psm-mock-webhook:local",
        context="scripts",
        dockerfile="tilt/Dockerfile.mock-webhook",
        only=["mock-webhook.go"],
    )

# ---------------------------------------------------------------------------
# Docker Compose
# ---------------------------------------------------------------------------

docker_compose("tilt/docker-compose.yaml")

# ---------------------------------------------------------------------------
# Resource configuration
# ---------------------------------------------------------------------------

dc_resource("monitor", labels=["settlement-monitor"])

if prometheus_enabled or grafana_enabled:
    dc_resource("prometheus", labels=["observability"])
if grafana_enabled:
    dc_resource("grafana", labels=["observability"])
if loki_enabled:
    dc_resource("loki", labels=["observability"])
    dc_resource("promtail", labels=["observability"])
if mock_webhook_enabled:
    dc_resource("mock-webhook", labels=["testing"])

# ---------------------------------------------------------------------------
# Quick links
# ---------------------------------------------------------------------------

print("=" * 60)
print("  Pocket Settlement Monitor — Tilt Dev Environment")
print("=" * 60)
print("")
print("  Monitor metrics:  http://localhost:%d/metrics" % port_monitor)
print("  Health check:     http://localhost:%d/health" % port_monitor)

if prometheus_enabled:
    print("  Prometheus:       http://localhost:%d" % port_prometheus)
    print("  Targets:          http://localhost:%d/targets" % port_prometheus)

if grafana_enabled:
    print("  Grafana:          http://localhost:%d" % port_grafana)
    print("  Dashboard:        http://localhost:%d/d/psm-settlement-monitor" % port_grafana)

if loki_enabled:
    print("  Loki:             http://localhost:%d" % port_loki)

if mock_webhook_enabled:
    print("  Mock webhook:     http://localhost:%d/requests" % port_mock_webhook)

print("")
print("=" * 60)
