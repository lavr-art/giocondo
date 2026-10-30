"""Upgrade an isolated bench node from a supplied legacy agent, preserving WG clients.

Run after run.sh completes. Supply a baseline Linux binary in bin/network-e2e/
(default name agent-legacy). This intentionally replaces only bench binaries.
"""
import importlib.util
import json
import pathlib
import subprocess
import time

spec = importlib.util.spec_from_file_location("network_bench", pathlib.Path(__file__).with_name("test.py"))
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)


def main():
    if not (bench.WORK / "agent-legacy").is_file():
        raise SystemExit("Build the previous Linux agent into bin/network-e2e/agent-legacy first")
    nodes = bench.api("GET", "nodes")
    active = nodes[0]
    service = {"172.30.77.11": "node1", "172.30.77.12": "node1", "172.30.77.21": "node2"}[active["ip"]]
    container = bench.run("ps", "-q", service).stdout.strip()
    subprocess.run(["docker", "update", "--cpus", "1", "--memory", "1g", "--memory-swap", "1g", container], check=True, capture_output=True)
    bench.run("cp", str(bench.WORK / "agent-legacy"), service + ":/tmp/giocondo-agent-legacy")
    bench.run("cp", str(bench.WORK / "agent"), service + ":/tmp/giocondo-agent-current")
    execute = lambda *args, **kwargs: bench.exec_(service, *args, **kwargs)
    execute("systemctl", "stop", "giocondo-vpn-agent")
    bench.wait("agent offline before binary replacement", lambda: not bench.api("GET", "nodes/" + str(active["id"]))["agent_connected"], 30)
    # Preserve prior test journals. A pre-runtime installation has no engine journal.
    execute("python3", "-c", "import pathlib,time; p=pathlib.Path('/var/lib/giocondo-vpn/agent/sing-box'); p.exists() and p.rename(p.with_name('sing-box-before-upgrade-'+str(time.time_ns())))")
    execute("install", "-m", "0755", "/tmp/giocondo-agent-legacy", "/usr/bin/giocondo-vpn-agent")
    execute("systemctl", "start", "giocondo-vpn-agent")
    bench.wait("legacy agent connected", lambda: bench.api("GET", "nodes/" + str(active["id"]))["agent_connected"], 30)
    bench.wait("legacy agent WG traffic", lambda: bench.client_request("wg"), 30)
    # Allow the legacy agent to synchronize, then freeze its state for comparison.
    time.sleep(3)
    config = execute("cat", "/etc/giocondo-vpn/sing-box/config.json").stdout
    pid = execute("systemctl", "show", "-p", "MainPID", "--value", "giocondo-vpn-sing-box").stdout.strip()
    started = execute("systemctl", "show", "-p", "ExecMainStartTimestampMonotonic", "--value", "giocondo-vpn-sing-box").stdout
    fingerprints = {p.name: p.read_bytes() for p in bench.WORK.glob("client-*.json")}
    execute("systemctl", "stop", "giocondo-vpn-agent")
    bench.wait("agent offline before binary replacement", lambda: not bench.api("GET", "nodes/" + str(active["id"]))["agent_connected"], 30)
    execute("install", "-m", "0755", "/tmp/giocondo-agent-current", "/usr/bin/giocondo-vpn-agent")
    execute("systemctl", "start", "giocondo-vpn-agent")
    bench.wait("upgraded agent connected", lambda: bench.api("GET", "nodes/" + str(active["id"]))["agent_connected"], 30)
    for kind in ("vless", "hy2", "wg"):
        bench.wait("unchanged client after agent upgrade " + kind, lambda kind=kind: bench.client_request(kind), 30)
    assert execute("cat", "/etc/giocondo-vpn/sing-box/config.json").stdout == config
    assert execute("systemctl", "show", "-p", "MainPID", "--value", "giocondo-vpn-sing-box").stdout.strip() == pid
    assert execute("systemctl", "show", "-p", "ExecMainStartTimestampMonotonic", "--value", "giocondo-vpn-sing-box").stdout == started
    assert {p.name: p.read_bytes() for p in bench.WORK.glob("client-*.json")} == fingerprints
    bench.REPORT.append("agent upgrade preserved config, client keys and sing-box process")
    rss = []
    agent_pid = int(execute("systemctl", "show", "-p", "MainPID", "--value", "giocondo-vpn-agent").stdout)
    for _ in range(5):
        status = execute("cat", f"/proc/{agent_pid}/status").stdout
        rss.append(int(next(line for line in status.splitlines() if line.startswith("VmRSS:")).split()[1]) * 1024)
        time.sleep(2)
    limit = int(execute("systemctl", "show", "-p", "MemoryMax", "--value", "giocondo-vpn-agent").stdout)
    assert limit == 64 * 1024 * 1024
    result = {"checks": bench.REPORT, "agent_rss_bytes": rss, "service_memory_max_bytes": limit, "node_cpus": 1, "node_memory_bytes": 1024 ** 3}
    (bench.WORK / "agent-upgrade-results.json").write_text(json.dumps(result, indent=2) + "\n")
    print("PASS agent upgrade without sing-box restart; RSS samples (MiB):", [round(n / 1024 ** 2, 1) for n in rss], flush=True)


if __name__ == "__main__":
    main()
