"""
ignite — Solidity Audit Agent

Usage:
    python ignite.py forge .
    python ignite.py forge /path/to/project
    python ignite.py forge . --reconfigure
    python ignite.py forge . --update       # git pull agent

Two-repo mode (keep agent code private):
    Set env var IGNITE_FORGE_REPO=https://github.com/you/sol-audit-forge
    ignite will clone the agent to ~/.ignite/agents/forge/ automatically.
    Without it: single-repo mode, agent lives next to this file.
"""

import json
import os
import platform
import shutil
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen

# ── Bootstrap rich ────────────────────────────────────────────────────────────

def _ensure(pkg: str, import_as: str | None = None):
    name = import_as or pkg
    try:
        return __import__(name)
    except ImportError:
        subprocess.check_call([sys.executable, "-m", "pip", "install", pkg, "-q"])
        return __import__(name)

_ensure("rich")
_ensure("questionary")

from rich.console import Console
from rich.panel import Panel
from rich.table import Table
from rich.text import Text
from rich import box
import questionary
from questionary import Style

console = Console()

Q_STYLE = Style([
    ("qmark",        "fg:#7c6f64 bold"),
    ("question",     "bold"),
    ("answer",       "fg:#b8bb26 bold"),
    ("pointer",      "fg:#fe8019 bold"),
    ("highlighted",  "fg:#fe8019 bold"),
    ("selected",     "fg:#b8bb26"),
    ("separator",    "fg:#665c54"),
    ("instruction",  "fg:#7c6f64"),
])


# ── Paths ─────────────────────────────────────────────────────────────────────

IGNITE_HOME = Path.home() / ".ignite"
IMAGE_PREFIX = "ignite"          # images named ignite-forge, ignite-hardhat, …


# ── Manager registry ──────────────────────────────────────────────────────────

MANAGERS: dict[str, dict] = {
    "forge": {
        "label":   "Foundry / Forge",
        "detect":  lambda p: (p / "foundry.toml").exists() or (p / "forge.toml").exists(),
        "env_var": "IGNITE_FORGE_REPO",   # set to enable two-repo mode
    },
    # future:
    # "hardhat": { "label": "Hardhat", "detect": lambda p: (p/"hardhat.config.js").exists(), ... },
}


# ── Provider / model registry ──────────────────────────────────────────────────

PROVIDERS = {
    "anthropic": {
        "label":            "Anthropic (Claude)",
        "reasoning_models": [
            "claude-sonnet-4-5",
            "claude-opus-4",
            "claude-opus-4-5",
            "claude-haiku-3-5",
        ],
        "local_model":      "claude-haiku-3-5",
        "env_key":          "ANTHROPIC_API_KEY",
        "provider_str":     "anthropic",
        "base_url":         None,
    },
    "openai": {
        "label":            "OpenAI (GPT / o-series)",
        "reasoning_models": [
            "gpt-4.1",
            "o3",
            "o4-mini",
            "gpt-4.1-mini",
            "gpt-4o",
        ],
        "local_model":      "gpt-4.1-mini",
        "env_key":          "OPENAI_API_KEY",
        "provider_str":     "openai",
        "base_url":         None,
    },
    "google": {
        "label":            "Google (Gemini)",
        "reasoning_models": [
            "gemini-2.5-pro",
            "gemini-2.5-flash",
            "gemini-2.0-flash",
        ],
        "local_model":      "gemini-2.0-flash",
        "env_key":          "GOOGLE_API_KEY",
        "provider_str":     "google",
        "base_url":         "https://generativelanguage.googleapis.com/v1beta/openai/",
    },
    "ollama": {
        "label":            "Ollama (local)",
        "reasoning_models": [],      # populated at runtime
        "local_model":      None,
        "env_key":          None,
        "provider_str":     "ollama",
        "base_url":         "http://localhost:11434",
    },
}

FALLBACK_OLLAMA_MODELS = [
    "qwen2.5-coder:14b",
    "qwen2.5-coder:7b",
    "deepseek-coder:33b",
    "deepseek-coder:6.7b",
    "llama3.1:8b",
    "codellama:13b",
]


# ── Detection helpers ──────────────────────────────────────────────────────────

def docker_running() -> bool:
    if not shutil.which("docker"):
        return False
    try:
        subprocess.run(["docker", "info"], capture_output=True, check=True, timeout=10)
        return True
    except Exception:
        return False


def get_ollama_models(base_url: str = "http://localhost:11434") -> list[str]:
    try:
        with urlopen(f"{base_url}/api/tags", timeout=4) as r:
            return [m["name"] for m in json.loads(r.read()).get("models", [])]
    except Exception:
        return []


# ── Two-repo / agent dir ──────────────────────────────────────────────────────

def get_agent_dir(manager: str) -> Path:
    """
    Single-repo: agent lives next to ignite.py.
    Two-repo: set IGNITE_<MANAGER>_REPO → agent cloned to ~/.ignite/agents/<manager>/
    """
    env_var = MANAGERS[manager]["env_var"]
    if os.environ.get(env_var):
        return IGNITE_HOME / "agents" / manager
    return Path(__file__).parent


def ensure_agent(manager: str, update: bool = False):
    env_var = MANAGERS[manager]["env_var"]
    repo_url = os.environ.get(env_var)
    if not repo_url:
        return   # single-repo, nothing to fetch

    agent_dir = get_agent_dir(manager)
    if not (agent_dir / ".git").exists():
        console.print(f"  [dim]Cloning agent from {repo_url} …[/dim]")
        agent_dir.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "clone", repo_url, str(agent_dir)], check=True)
    elif update:
        console.print("  [dim]Updating agent …[/dim]")
        subprocess.run(["git", "pull", "--ff-only"], cwd=agent_dir)


# ── Interactive setup ──────────────────────────────────────────────────────────

def _ask_reasoning() -> tuple[str, str, str | None]:
    """Returns (provider_str, model_name, base_url_or_None)."""

    provider_label = questionary.select(
        "Select provider:",
        choices=[p["label"] for p in PROVIDERS.values()],
        style=Q_STYLE,
    ).ask()
    if provider_label is None:
        sys.exit(0)

    prov_key = next(k for k, v in PROVIDERS.items() if v["label"] == provider_label)
    prov = PROVIDERS[prov_key]

    console.print()
    console.rule("[dim]STEP 2 — model[/dim]")

    if prov_key == "ollama":
        base_url = questionary.text(
            "Ollama URL:", default=prov["base_url"], style=Q_STYLE
        ).ask() or prov["base_url"]
        models = get_ollama_models(base_url)
        choices = (models or FALLBACK_OLLAMA_MODELS) + ["[ enter manually ]"]
        if models:
            console.print(f"  [green]✔[/green]  Found {len(models)} local model(s)")
        else:
            console.print("  [yellow]⚠[/yellow]  Ollama unreachable — showing defaults")
        model = questionary.select("Select local model:", choices=choices, style=Q_STYLE).ask()
        if model == "[ enter manually ]" or model is None:
            model = questionary.text("Model name:").ask() or FALLBACK_OLLAMA_MODELS[0]
        return prov["provider_str"], model, base_url

    model = questionary.select(
        f"Select model:",
        choices=prov["reasoning_models"],
        style=Q_STYLE,
    ).ask()
    if model is None:
        sys.exit(0)

    return prov["provider_str"], model, prov.get("base_url")


def _ask_local(reasoning_provider: str, reasoning_model: str) -> tuple[str, str, str | None]:
    """Returns (provider_str, model_name, base_url_or_None) for the local/flash slot."""

    if reasoning_provider == "ollama":
        prov = PROVIDERS["ollama"]
        return "ollama", reasoning_model, prov["base_url"]

    console.print()
    console.rule("[dim]STEP 3 — local model  (fast tasks, runs often)[/dim]")

    # Check Ollama
    ollama_models = get_ollama_models()
    if ollama_models:
        console.print(f"  [green]✔[/green]  Ollama detected  ({ollama_models[0]})")
        use_local = questionary.confirm(
            "Use Ollama for fast tasks (free)?", default=True, style=Q_STYLE
        ).ask()
        if use_local:
            choices = ollama_models + ["[ enter manually ]"]
            model = questionary.select("Select local model:", choices=choices, style=Q_STYLE).ask()
            if model == "[ enter manually ]" or model is None:
                model = questionary.text("Model name:").ask() or ollama_models[0]
            # Inside Docker, host Ollama is at host.docker.internal
            return "ollama", model, "http://host.docker.internal:11434"
    else:
        console.print("  [dim]Ollama not detected on localhost:11434[/dim]")

    # Fall back to provider cheap tier
    prov = PROVIDERS.get(reasoning_provider, {})
    fallback = prov.get("local_model") or reasoning_model
    console.print(f"  [dim]Using {fallback} as local model[/dim]")
    return reasoning_provider, fallback, prov.get("base_url")


def _ask_api_key(provider_str: str) -> str:
    prov = next((p for p in PROVIDERS.values() if p["provider_str"] == provider_str), {})
    env_key = prov.get("env_key")
    if not env_key:
        return ""
    console.print()
    console.rule("[dim]STEP 4 — API key[/dim]")
    key = questionary.password(f"{env_key}:", style=Q_STYLE).ask() or ""
    if not key:
        console.print(f"  [yellow]⚠[/yellow]  No key entered — set {env_key} in .env later")
    return key


def run_setup(manager: str) -> dict:
    """Interactive setup. Writes config.json + .env. Returns config dict."""
    config_path = IGNITE_HOME / "config.json"
    env_path    = IGNITE_HOME / ".env"
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)

    console.print()
    console.rule("[bold]STEP 1 — reasoning model[/bold]")

    r_provider, r_model, r_base_url = _ask_reasoning()
    api_key = _ask_api_key(r_provider)
    l_provider, l_model, l_base_url = _ask_local(r_provider, r_model)

    # ── Build config ──────────────────────────────────────────────────────────

    local_entry: dict = {
        "provider":    l_provider,
        "model":       l_model,
        "temperature": 0.1,
        "max_tokens":  4096,
    }
    if l_base_url:
        local_entry["base_url"] = l_base_url

    reasoning_entry: dict = {
        "provider":    r_provider,
        "model":       r_model,
        "temperature": 0.3,
        "max_tokens":  8192,
    }
    if r_base_url:
        reasoning_entry["base_url"] = r_base_url

    config = {
        "models": {
            "local":     local_entry,
            "reasoning": reasoning_entry,
        },
        "task_routing": {
            "default":       "local",
            "deep_reasoning": "reasoning",
        },
    }

    config_path.write_text(json.dumps(config, indent=2))
    console.print(f"\n  [green]✔[/green]  config.json  →  {config_path}")

    env_lines = ["# Generated by ignite — do not commit", ""]
    if api_key:
        prov = next((p for p in PROVIDERS.values() if p["provider_str"] == r_provider), {})
        if prov.get("env_key"):
            env_lines.append(f"{prov['env_key']}={api_key}")
    env_path.write_text("\n".join(env_lines) + "\n")
    console.print(f"  [green]✔[/green]  .env         →  {env_path}")

    return config


# ── Docker ────────────────────────────────────────────────────────────────────

def build_image(manager: str, agent_dir: Path):
    image = f"{IMAGE_PREFIX}-{manager}"
    console.print(f"\n  [dim]Building {image} …[/dim]")
    r = subprocess.run(["docker", "build", "-t", image, "."], cwd=agent_dir)
    if r.returncode != 0:
        console.print("  [red]✗[/red]  Docker build failed")
        sys.exit(1)
    console.print(f"  [green]✔[/green]  Image built: {image}")


def image_exists(manager: str) -> bool:
    r = subprocess.run(
        ["docker", "image", "inspect", f"{IMAGE_PREFIX}-{manager}"],
        capture_output=True,
    )
    return r.returncode == 0


def load_env_vars() -> dict[str, str]:
    env_path = IGNITE_HOME / ".env"
    skip = {"PROJECT_PATH"}
    env: dict[str, str] = {}
    if env_path.exists():
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, _, v = line.partition("=")
                if k not in skip and v:
                    env[k] = v
    # Also pull from current shell environment
    for key in ("ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY"):
        if key in os.environ and key not in env:
            env[key] = os.environ[key]
    return env


def run_container(manager: str, project_path: Path, config_path: Path) -> int:
    image     = f"{IMAGE_PREFIX}-{manager}"
    env_vars  = load_env_vars()
    extra_hosts = (
        ["--add-host", "host.docker.internal:host-gateway"]
        if platform.system() == "Linux" else []
    )
    env_flags = []
    for k, v in env_vars.items():
        env_flags += ["-e", f"{k}={v}"]

    cmd = [
        "docker", "run", "--rm",
        "-v", f"{project_path}:/project",
        "-v", f"{config_path}:/app/config.json:ro",
        "-e", "PROJECT_PATH=/project",
        *env_flags,
        *extra_hosts,
        image,
    ]
    console.print(f"\n  [dim]$ {' '.join(cmd)}[/dim]\n")
    return subprocess.run(cmd).returncode


# ── Report display ─────────────────────────────────────────────────────────────

SEVERITY_COLOR = {
    "high":          "bold red",
    "medium":        "bold yellow",
    "low":           "bold cyan",
    "informational": "dim",
}

def print_report(project_path: Path):
    results_path = project_path / "audit_results.json"
    report_path  = project_path / "audit_report.md"

    if not results_path.exists():
        console.print("  [yellow]⚠[/yellow]  audit_results.json not found")
        return

    data = json.loads(results_path.read_text())

    # Aggregate across steps
    buckets: dict[str, list] = {"high": [], "medium": [], "low": [], "informational": []}
    for step_data in data.get("steps", {}).values():
        for sev in buckets:
            buckets[sev].extend(step_data.get("findings", {}).get(sev, []))

    total = sum(len(v) for v in buckets.values())
    h, m  = len(buckets["high"]), len(buckets["medium"])

    # Header panel
    project_name = project_path.name
    date_str     = datetime.now().strftime("%Y-%m-%d")
    header_text  = Text.assemble(
        ("SECURITY AUDIT REPORT\n", "bold"),
        (f"Project: {project_name}   ", ""),
        (f"Date: {date_str}\n", "dim"),
        (f"{h} High  ·  {m} Medium  ·  {total} total", "bold"),
    )
    console.print(Panel(header_text, box=box.DOUBLE_EDGE, padding=(0, 2)))

    if total == 0:
        console.print("\n  [bold green]No findings detected.[/bold green]\n")
        return

    # One table per severity that has findings
    for sev, items in buckets.items():
        if not items:
            continue
        color = SEVERITY_COLOR.get(sev, "")
        prefix = sev[0].upper()
        label  = sev.upper()

        console.print(f"\n  [{color}]● {label}[/{color}]  ({len(items)} finding{'s' if len(items)!=1 else ''})\n")

        tbl = Table(box=box.SIMPLE, show_header=True, header_style="dim",
                    padding=(0, 1), show_edge=False)
        tbl.add_column("ID",           style="dim",   width=5,  no_wrap=True)
        tbl.add_column("Description",  style="",      min_width=36)
        tbl.add_column("Location",     style="dim",   width=22, no_wrap=True)

        for idx, item in enumerate(items, 1):
            row_id   = f"{prefix}-{idx:02d}"
            desc     = (item.get("title") or item.get("check") or
                        item.get("description", "—"))[:72]
            location = item.get("location", "—")[:28]
            tbl.add_row(row_id, desc, location)

        console.print(tbl)

    # Footer
    console.print()
    if report_path.exists():
        console.print(f"  [green]Full report →[/green] {report_path}")
    else:
        console.print("  [yellow]audit_report.md not generated[/yellow]")
    console.print()


# ── Entry point ────────────────────────────────────────────────────────────────

def usage():
    console.print(
        Panel(
            "[bold]ignite[/bold] — Solidity Audit Agent\n\n"
            "  [green]python ignite.py forge .[/green]\n"
            "  python ignite.py forge /path/to/project\n"
            "  python ignite.py forge . [dim]--reconfigure[/dim]\n\n"
            f"  Available managers: {', '.join(MANAGERS)}",
            title="Usage", box=box.ROUNDED,
        )
    )


def main():
    args        = sys.argv[1:]
    flags       = {a for a in args if a.startswith("--")}
    positional  = [a for a in args if not a.startswith("--")]

    reconfigure = "--reconfigure" in flags
    do_update   = "--update"      in flags

    if len(positional) < 2:
        usage()
        sys.exit(1)

    manager      = positional[0].lower()
    project_path = Path(positional[1]).resolve()

    if manager not in MANAGERS:
        console.print(f"  [red]✗[/red]  Unknown manager '{manager}'. "
                      f"Available: {', '.join(MANAGERS)}")
        sys.exit(1)

    if not project_path.exists():
        console.print(f"  [red]✗[/red]  Path not found: {project_path}")
        sys.exit(1)

    # ── Status line ───────────────────────────────────────────────────────────

    config_path = IGNITE_HOME / "config.json"
    console.print()

    if config_path.exists() and not reconfigure:
        cfg     = json.loads(config_path.read_text())
        r_model = cfg.get("models", {}).get("reasoning", {}).get("model", "?")
        console.print(f"  [green]✔[/green]  Config found  [dim](reasoning: {r_model})[/dim]")
    else:
        if reconfigure:
            console.print("  [dim]Reconfiguring …[/dim]")
        else:
            console.print("  [yellow]No config found.[/yellow]")

        mdef = MANAGERS[manager]
        if mdef["detect"](project_path):
            console.print(f"  [green]✔[/green]  Detected {mdef['label']} project")
        else:
            console.print(f"  [yellow]⚠[/yellow]  No {mdef['label']} config found in {project_path}")

        if not docker_running():
            console.print("  [red]✗[/red]  Docker is not running. Start Docker Desktop and retry.")
            sys.exit(1)
        console.print("  [green]✔[/green]  Docker available")

        run_setup(manager)

    # ── Agent + image ─────────────────────────────────────────────────────────

    ensure_agent(manager, update=do_update)
    agent_dir = get_agent_dir(manager)

    if not image_exists(manager) or reconfigure:
        build_image(manager, agent_dir)

    # ── Run ───────────────────────────────────────────────────────────────────

    console.rule()
    console.print(f"\n  [bold]Running audit …[/bold]  [dim]{project_path}[/dim]\n")

    rc = run_container(manager, project_path, config_path)
    if rc != 0:
        console.print(f"  [red]✗[/red]  Container exited with code {rc}")

    print_report(project_path)


if __name__ == "__main__":
    main()