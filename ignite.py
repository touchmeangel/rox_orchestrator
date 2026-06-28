import json
import os
import platform
import re
import shutil
import subprocess
import sys
from pathlib import Path
from urllib.request import urlopen

import questionary
from questionary import Style
from rich import box
from rich.console import Console
from rich.live import Live
from rich.panel import Panel
from rich.spinner import Spinner
from rich.text import Text

IGNITE_HOME = Path.home() / ".ignite"

console = Console()

Q_STYLE = Style(
    [
        ("qmark", "fg:cyan bold"),
        ("question", "bold"),
        ("answer", "fg:cyan bold"),
        ("pointer", "fg:cyan bold"),
        ("highlighted", "fg:cyan bold"),
        ("selected", "fg:cyan"),
        ("separator", "fg:#665c54"),
        ("instruction", "fg:#7c6f64"),
    ]
)

MANAGERS: dict[str, dict] = {
    "foundry": {
        "label": "Foundry",
        "image": "touchmeangel/ignite_agent:latest",
    }
}

PROVIDERS: dict[str, dict] = {
    "anthropic": {
        "label": "Anthropic",
        "models": [
            {"id": "claude-opus-4-8", "supports_reasoning_effort": True},
            {"id": "claude-opus-4-7", "supports_reasoning_effort": True},
            {"id": "claude-opus-4-6", "supports_reasoning_effort": True},
            {"id": "claude-sonnet-4-6", "supports_reasoning_effort": True},
            {"id": "claude-haiku-4-5", "supports_reasoning_effort": False},
        ],
        "env_key": "ANTHROPIC_API_KEY",
        "provider_str": "anthropic",
        "base_url": None,
        "effort_levels": ["low", "medium", "high", "xhigh", "max"],
        "default_effort": "high",
    },
    "openai": {
        "label": "OpenAI",
        "models": [
            {"id": "gpt-5.5", "supports_reasoning_effort": True},
            {"id": "gpt-5", "supports_reasoning_effort": True},
            {"id": "gpt-4.1-mini", "supports_reasoning_effort": False},
            {"id": "gpt-4o", "supports_reasoning_effort": False},
        ],
        "env_key": "OPENAI_API_KEY",
        "provider_str": "openai",
        "base_url": None,
        "effort_levels": ["none", "low", "medium", "high", "xhigh"],
        "default_effort": "medium",
    },
    "google": {
        "label": "Google",
        "models": [
            {"id": "gemini-3.5-flash", "supports_reasoning_effort": True},
            {"id": "gemini-3.1-pro", "supports_reasoning_effort": True},
            {"id": "gemini-3.1-flash-lite", "supports_reasoning_effort": True},
            {"id": "gemini-2.5-flash", "supports_reasoning_effort": True},
        ],
        "env_key": "GOOGLE_API_KEY",
        "provider_str": "openai",
        "base_url": "https://generativelanguage.googleapis.com/v1beta/openai/",
        "effort_levels": ["low", "medium", "high"],
        "default_effort": "medium",
    },
    "openrouter": {
        "label": "OpenRouter",
        "models": [],
        "env_key": "OPENROUTER_API_KEY",
        "provider_str": "openai",
        "base_url": "https://openrouter.ai/api/v1",
        "effort_levels": ["none", "minimal", "low", "medium", "high", "xhigh"],
        "default_effort": "medium",
    },
    "ollama": {
        "label": "Ollama (local)",
        "models": [],
        "env_key": None,
        "provider_str": "openai",
        "base_url": "http://localhost:11434/v1",
        "effort_levels": [],
        "default_effort": None,
    },
}

FALLBACK_OLLAMA_MODELS: list[str] = []
CUSTOM_MODEL_LABEL = "[ enter custom model id ]"
_BACK = "← Go back"


def handle_abort() -> None:
    console.print(
        "\n\n  [dim]⚠[/dim]  [bold]Execution cancelled by user. Exiting...[/bold]\n"
    )
    sys.exit(130)


def docker_running() -> bool:
    docker_path = shutil.which("docker")
    if not docker_path:
        return False
    try:
        subprocess.run(  # noqa: S603
            [docker_path, "info"], capture_output=True, check=True, timeout=10
        )
        return True
    except Exception:
        return False


def _git_repo_root(path: Path) -> Path | None:
    git_path = shutil.which("git")
    if not git_path:
        return None
    try:
        result = subprocess.run(  # noqa: S603
            [git_path, "rev-parse", "--show-toplevel"],
            capture_output=True,
            text=True,
            cwd=str(path),
            timeout=5,
        )
        if result.returncode == 0:
            return Path(result.stdout.strip())
    except Exception:
        pass
    return None


def _git_remote_url(repo_root: Path) -> str | None:
    git_path = shutil.which("git")
    if not git_path:
        return None
    try:
        result = subprocess.run(  # noqa: S603
            [git_path, "remote", "get-url", "origin"],
            capture_output=True,
            text=True,
            cwd=str(repo_root),
            timeout=5,
        )
        if result.returncode == 0:
            return result.stdout.strip() or None
    except Exception:
        pass
    return None


def _repo_slug(source: str) -> str:
    source = source.rstrip("/").removesuffix(".git")
    parts = source.replace("\\", "/").split("/")
    if any(host in source for host in ("github.com", "gitlab.com", "bitbucket.org")):
        slug = "_".join(parts[-2:]) if len(parts) >= 2 else parts[-1]
    else:
        slug = parts[-1]
    return re.sub(r"[^a-zA-Z0-9_-]", "_", slug) or "repo"


def _clone_to_cache(github_url: str, force: bool = False) -> Path:
    slug = _repo_slug(github_url)
    repo_path = IGNITE_HOME / "repos" / slug

    if repo_path.exists() and not force:
        console.print(
            f"  [cyan]✔[/cyan]  Cached clone  [dim]{repo_path}[/dim]  "
            "[dim](--fresh to re-clone)[/dim]"
        )
        return repo_path

    if repo_path.exists() and force:
        shutil.rmtree(repo_path)

    repo_path.mkdir(parents=True, exist_ok=True)
    git_path = shutil.which("git") or "git"
    console.print(f"  [dim]Cloning {github_url} …[/dim]", highlight=False)

    result = subprocess.run(  # noqa: S603
        [git_path, "clone", "--depth", "1", github_url, str(repo_path)],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        shutil.rmtree(repo_path, ignore_errors=True)
        console.print(f"  [red]✗[/red]  git clone failed:\n{result.stderr.strip()}")
        sys.exit(1)

    console.print(f"  [cyan]✔[/cyan]  Cloned  [dim]{repo_path}[/dim]")
    return repo_path


def _prepare_repo(
    github_url: str | None,
    path: Path,
    force_reclone: bool = False,
) -> tuple[Path, str]:
    if github_url:
        repo_path = _clone_to_cache(github_url, force=force_reclone)
        return repo_path, _repo_slug(github_url)

    git_root = _git_repo_root(path)
    if git_root:
        remote_url = _git_remote_url(git_root)
        display = remote_url if remote_url else str(git_root)
        console.print(f"  [cyan]✔[/cyan]  Git repo  [dim]{display}[/dim]")
        slug = _repo_slug(remote_url) if remote_url else _repo_slug(str(git_root))
        return git_root, slug

    console.print(f"  [dim]⚠  No git repository found at {path}[/dim]")
    choice = questionary.select(
        "How would you like to proceed?",
        choices=[
            "Use this directory as source",
            "Enter a GitHub URL to clone",
        ],
        style=Q_STYLE,
    ).ask()
    if choice is None:
        handle_abort()

    if "GitHub URL" in choice:
        entered = questionary.text("GitHub URL:", style=Q_STYLE).ask()
        if not entered:
            handle_abort()
        entered = entered.strip()
        repo_path = _clone_to_cache(entered, force=force_reclone)
        return repo_path, _repo_slug(entered)

    slug = _repo_slug(str(path))
    console.print(f"  [cyan]✔[/cyan]  Using directory as-is  [dim]{path}[/dim]")
    return path, slug


def get_ollama_models(base_url: str = "http://localhost:11434") -> list[str]:
    api_base = base_url.rstrip("/")
    if api_base.endswith("/v1"):
        api_base = api_base[:-3]
    if not (api_base.startswith("http://") or api_base.startswith("https://")):
        return []
    try:
        with urlopen(f"{api_base}/api/tags", timeout=4) as r:  # noqa: S310
            models = [m["name"] for m in json.loads(r.read()).get("models", [])]

        tools_capable = []
        for model in models:
            try:
                result = subprocess.run(  # noqa: S603
                    ["ollama", "show", model, "--modelfile"],  # noqa: S607
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                if (
                    "{{ if .Tools }}" in result.stdout
                    or "tools" in result.stdout.lower()
                ):
                    tools_capable.append(model)
                else:
                    tools_capable.append(
                        model + "  [dim](⚠ tools capability unconfirmed)[/dim]"
                    )
            except Exception:
                tools_capable.append(model)
        return tools_capable
    except Exception:
        return []


def _resolve_model_caps(model_id: str, prov: dict) -> dict:
    known = {m["id"]: m for m in prov.get("models", [])}
    if model_id in known:
        return known[model_id]

    console.print(f"  [dim]No capability info for '{model_id}' — please declare:[/dim]")
    choice = questionary.select(
        "This model accepts:",
        choices=["temperature", "reasoning_effort"],
        style=Q_STYLE,
    ).ask()
    if choice is None:
        handle_abort()
    return {
        "id": model_id,
        "supports_reasoning_effort": choice == "reasoning_effort",
    }


def build_model_params(caps: dict, effort: str | None) -> dict:
    if caps.get("supports_reasoning_effort") and effort:
        return {"reasoning_effort": effort}
    return {"temperature": 0.3}


def _ask_api_key(env_key: str) -> str:
    if not env_key:
        return ""
    key = questionary.password(f"{env_key}:", style=Q_STYLE).ask()
    if key is None:
        handle_abort()
    if not key:
        console.print(
            f"  [dim]⚠  Value omitted — initialize {env_key} inside environment values later[/dim]"
        )
    return key  # type: ignore[return-value]


def _ask_effort(prov: dict) -> str:
    console.print(
        "  [dim]Controls thinking depth. Can be changed later with -r (reconfigure).[/dim]"
    )
    effort = questionary.select(
        "Reasoning effort:",
        choices=prov["effort_levels"],
        default=prov["default_effort"],
        style=Q_STYLE,
    ).ask()
    if effort is None:
        handle_abort()
    return effort  # type: ignore[return-value]


def _ask_model_profile(
    existing: dict,
) -> tuple[str, str, str, str | None, str | None, dict, str | None]:
    while True:
        provider_label = questionary.select(
            "Select provider:",
            choices=[p["label"] for p in PROVIDERS.values()],
            style=Q_STYLE,
        ).ask()
        if provider_label is None:
            handle_abort()

        prov_key = next(k for k, v in PROVIDERS.items() if v["label"] == provider_label)
        prov = PROVIDERS[prov_key]

        same_provider = existing.get("provider_key") == prov_key

        if prov_key == "ollama":
            ollama_default = prov["base_url"] or "http://localhost:11434/v1"
            prev_url = existing.get("base_url", ollama_default) if same_provider else ollama_default
            base_url = questionary.text(
                "Ollama endpoint address:",
                default=prev_url or ollama_default,
                style=Q_STYLE,
            ).ask()
            if base_url is None:
                handle_abort()
            base_url = base_url or prev_url

            container_url = (
                (base_url or "")
                .replace("localhost", "host.docker.internal")
                .replace("127.0.0.1", "host.docker.internal")
            )

            models = get_ollama_models(base_url or "")
            model = questionary.select(
                "Select model tag:",
                choices=(models or FALLBACK_OLLAMA_MODELS)
                + [CUSTOM_MODEL_LABEL, _BACK],
                style=Q_STYLE,
            ).ask()
            if model is None:
                handle_abort()
            if model == _BACK:
                continue
            if "  [dim]" in model:
                model = model.split("  ")[0]
            if model == CUSTOM_MODEL_LABEL:
                model = questionary.text("Model tag:", style=Q_STYLE).ask()
                if model is None:
                    handle_abort()
                model = model or (
                    FALLBACK_OLLAMA_MODELS[0] if FALLBACK_OLLAMA_MODELS else ""
                )

            caps = _resolve_model_caps(model, prov)
            effort: str | None = None
            if caps.get("supports_reasoning_effort"):
                if prov["effort_levels"]:
                    effort = _ask_effort(prov)
                else:
                    console.print(
                        "  [dim]reasoning_effort declared but no levels defined for Ollama — "
                        "the value will be passed through as-is[/dim]"
                    )
                    effort = questionary.text(
                        "Effort value:", default="medium", style=Q_STYLE
                    ).ask()
                    if effort is None:
                        handle_abort()
            else:
                console.print("  [dim]Using temperature=0.7[/dim]")

            return (
                prov_key,
                prov["provider_str"],
                model,
                container_url,
                prov["env_key"],
                caps,
                effort,
            )

        if prov_key == "openrouter":
            prev_model = existing.get("model", "") if same_provider else ""
            model = questionary.text(
                "Model ID:",
                default=prev_model,
                style=Q_STYLE,
            ).ask()
            if model is None:
                handle_abort()
            if not model:
                continue

            caps = {"id": model, "supports_reasoning_effort": True}
            effort = _ask_effort(prov)
            return (
                prov_key,
                prov["provider_str"],
                model,
                prov["base_url"],
                prov["env_key"],
                caps,
                effort,
            )

        model_ids = [m["id"] for m in prov["models"]]
        selected = questionary.select(
            "Select a model variant:",
            choices=model_ids + [CUSTOM_MODEL_LABEL, _BACK],
            style=Q_STYLE,
        ).ask()
        if selected is None:
            handle_abort()
        if selected == _BACK:
            continue
        if selected == CUSTOM_MODEL_LABEL:
            selected = questionary.text("Model signature string:", style=Q_STYLE).ask()
            if not selected:
                handle_abort()

        caps = _resolve_model_caps(selected, prov)
        effort = None
        if caps.get("supports_reasoning_effort"):
            effort = _ask_effort(prov)
        else:
            console.print(
                "  [dim]Using temperature=0.7 (this model does not support reasoning effort)[/dim]"
            )

        return (
            prov_key,
            prov["provider_str"],
            selected,
            prov.get("base_url"),
            prov.get("env_key"),
            caps,
            effort,
        )


def _load_env_file() -> dict[str, str]:
    env_path = IGNITE_HOME / ".env"
    env: dict[str, str] = {}
    if env_path.exists():
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, _, v = line.partition("=")
                if v:
                    env[k] = v
    return env


def run_setup() -> dict:
    config_path = IGNITE_HOME / "config.json"
    env_path = IGNITE_HOME / ".env"
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)

    existing: dict = {}
    if config_path.exists():
        try:
            existing = json.loads(config_path.read_text())
        except Exception:
            pass

    existing_model = existing.get("models", {}).get("reasoning", {})

    console.print()
    console.rule("[dim]STEP 1 — Model[/dim]", style="dim")
    prov_key, provider_str, model, base_url, env_key, caps, effort = _ask_model_profile(
        existing_model
    )

    console.print()
    console.rule("[dim]STEP 2 — API Key[/dim]", style="dim")

    stored_keys = _load_env_file()
    collected_keys = dict(stored_keys)

    if env_key and env_key != "OLLAMA_API_KEY":
        if stored_keys.get(env_key):
            keep = questionary.confirm(
                f"Keep existing {env_key}?", default=True, style=Q_STYLE
            ).ask()
            if keep is None:
                handle_abort()
            if not keep:
                token = _ask_api_key(env_key)
                if token:
                    collected_keys[env_key] = token
        else:
            token = _ask_api_key(env_key)
            if token:
                collected_keys[env_key] = token
    elif env_key == "OLLAMA_API_KEY":
        collected_keys[env_key] = "ollama"

    gen_params = build_model_params(caps, effort)
    entry: dict = {
        "provider_key": prov_key,
        "provider": provider_str,
        "model": model,
        "max_tokens": 8192,
        **gen_params,
    }
    if base_url:
        entry["base_url"] = base_url
    if env_key:
        entry["api_key_env"] = env_key

    config = {
        "models": {"reasoning": entry},
        "task_routing": {"reasoning": "reasoning"},
    }
    config_path.write_text(json.dumps(config, indent=2))
    console.print(f"\n  [cyan]✔[/cyan]  config.json  →  {config_path}", highlight=False)

    env_lines = ["# Generated by ignite — do not commit", ""]
    for k, v in collected_keys.items():
        if v:
            env_lines.append(f"{k}={v}")
    env_path.write_text("\n".join(env_lines) + "\n")
    console.print(f"  [cyan]✔[/cyan]  .env         →  {env_path}", highlight=False)

    return config


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

    target_keys = (
        "ANTHROPIC_API_KEY",
        "OPENAI_API_KEY",
        "GOOGLE_API_KEY",
        "OLLAMA_API_KEY",
        "OPENROUTER_API_KEY",
    )
    for key in target_keys:
        if key in os.environ and key not in env:
            env[key] = os.environ[key]
    return env


def pull_image() -> None:
    docker_path = shutil.which("docker") or "docker"
    image = MANAGERS["foundry"]["image"]
    console.print(f"  [dim]Pulling latest image: {image} …[/dim]", highlight=False)
    subprocess.run([docker_path, "pull", image])  # noqa: S603, S607
    console.print("  [cyan]✔[/cyan]  Image updated.")


def _ensure_debug_file(debug_path: Path) -> None:
    if debug_path.is_dir():
        try:
            debug_path.rmdir()
        except OSError:
            console.print(
                f"  [red]✗[/red]  {debug_path} is a non-empty directory — "
                f"remove it manually and retry:\n      rm -r {debug_path}"
            )
            sys.exit(1)
    debug_path.parent.mkdir(parents=True, exist_ok=True)
    debug_path.touch(exist_ok=True)


def run_container(
    repo_path: Path,
    work_path: Path,
    config_path: Path,
    debug_path: Path,
    skip_build: bool = False,
) -> int:
    _ensure_debug_file(debug_path)

    docker_path = shutil.which("docker") or "docker"
    image = MANAGERS["foundry"]["image"]
    env_vars = load_env_vars()

    extra_hosts = (
        ["--add-host", "host.docker.internal:host-gateway"]
        if platform.system() == "Linux"
        else []
    )

    env_flags: list[str] = []
    for k, v in env_vars.items():
        env_flags += ["-e", f"{k}={v}"]

    user_mapping: list[str] = []
    if platform.system() != "Windows":
        user_mapping = ["--user", f"{os.getuid()}:{os.getgid()}"]

    container_args = [
        "--repo-path",
        "/repo",
        "--work-path",
        "/work",
        "--output",
        "/work/agent_results.json",
        "--debug",
        "/app/debug.log",
    ]
    if skip_build:
        container_args.append("--skip-build")

    cmd = [
        docker_path,
        "run",
        "--rm",
        "-t",
        "-v",
        f"{repo_path}:/repo:ro",
        "-v",
        f"{work_path}:/work:rw",
        "-v",
        f"{config_path}:/app/config.json:ro",
        "-v",
        f"{debug_path}:/app/debug.log:rw",
        *user_mapping,
        *env_flags,
        *extra_hosts,
        image,
        *container_args,
    ]

    console.print(f"  [dim]$ {' '.join(cmd)}[/dim]\n", highlight=False)

    process = subprocess.Popen(  # noqa: S603
        cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, bufsize=0
    )

    try:
        custom_spinner = Spinner("dots", text="Initializing container...", style="cyan")
        custom_spinner.frames = ["·", "*", "✷", "✸", "✹", "✺", "✹", "✸", "✷", "*"]
        custom_spinner.interval = 120

        with Live(
            custom_spinner, console=console, refresh_per_second=20, transient=True
        ) as live:
            is_first_line = True
            while True:
                chunk = process.stdout.read(1)
                if not chunk and process.poll() is not None:
                    break
                if chunk:
                    if is_first_line:
                        live.stop()
                        is_first_line = False
                    sys.stdout.buffer.write(chunk)
                    sys.stdout.buffer.flush()

    except KeyboardInterrupt:
        process.terminate()
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            process.kill()
        raise

    return process.returncode


def usage() -> None:
    console.print(
        Panel(
            "[bold]ignite[/bold] — EVM security research agent\n\n"
            "  [cyan]ignite[/cyan]                        [dim]Run in current directory (auto-detects git)[/dim]\n"
            "  [cyan]ignite /path/to/repo[/cyan]          [dim]Explicit local path[/dim]\n"
            "  [cyan]ignite --github-url <url>[/cyan]     [dim]Clone and audit a public repo[/dim]\n\n"
            "  [dim]-r  --reconfigure[/dim]               Reconfigure model / API key\n"
            "  [dim]-u  --update[/dim]                    Pull latest Docker image before running\n"
            "  [dim]    --fresh[/dim]                     Force re-clone even if cache exists\n"
            "  [dim]    --skip-build[/dim]                Skip build phase, re-run analysis only\n"
            "  [dim]-h  --help[/dim]",
            title="Usage",
            box=box.ROUNDED,
        )
    )


def main() -> None:
    try:
        args = sys.argv[1:]

        github_url: str | None = None
        for flag in ("--github-url", "-g"):
            if flag in args:
                try:
                    idx = args.index(flag)
                    github_url = args[idx + 1]
                    del args[idx : idx + 2]
                    break
                except IndexError:
                    console.print(f"  [red]✗[/red]  Missing value for {flag}.")
                    sys.exit(1)

        flags = {a for a in args if a.startswith("-")}
        positional = [a for a in args if not a.startswith("-")]

        if "--help" in flags or "-h" in flags:
            usage()
            sys.exit(0)

        reconfigure = "--reconfigure" in flags or "-r" in flags
        do_update = "--update" in flags or "-u" in flags
        force_reclone = "--fresh" in flags
        skip_build = "--skip-build" in flags

        if len(positional) > 1:
            usage()
            sys.exit(1)

        inspect_path = (
            Path(positional[0]).resolve() if positional else Path(".").resolve()
        )

        if not inspect_path.exists():
            console.print(
                f"  [red]✗[/red]  Path not found: {inspect_path}", highlight=False
            )
            sys.exit(1)

        debug_path = IGNITE_HOME / "debug.log"
        config_path = IGNITE_HOME / "config.json"

        console.print(f"  [dim]➔ Debug:[/dim] [cyan]{debug_path}[/cyan]")
        console.print("  [dim]➔ Pipeline:[/dim] [cyan]Foundry[/cyan]")
        console.print()

        if config_path.exists() and not reconfigure:
            cfg = json.loads(config_path.read_text())
            r_model = cfg.get("models", {}).get("reasoning", {}).get("model", "?")
            r_effort = (
                cfg.get("models", {}).get("reasoning", {}).get("reasoning_effort")
            )
            effort_hint = f", effort: {r_effort}" if r_effort else ""
            console.print(
                f"  [cyan]✔[/cyan]  Config  [dim](model: {r_model}{effort_hint})[/dim]"
            )
        else:
            if reconfigure:
                console.print("  [dim]Reconfiguring …[/dim]")
            else:
                console.print(
                    "  [dim]No config found — running first-time setup.[/dim]"
                )

            console.print(
                f"  [cyan]✔[/cyan]  Using {MANAGERS['foundry']['label']} pipeline"
            )

            if not docker_running():
                console.print(
                    "  [red]✗[/red]  Docker is not running. Start Docker Desktop and retry."
                )
                sys.exit(1)
            console.print("  [cyan]✔[/cyan]  Docker available")

            run_setup()

        if do_update:
            pull_image()

        repo_path, slug = _prepare_repo(
            github_url=github_url,
            path=inspect_path,
            force_reclone=force_reclone,
        )

        work_path = IGNITE_HOME / "workspaces" / slug
        work_path.mkdir(parents=True, exist_ok=True)
        console.print(f"  [cyan]✔[/cyan]  Workspace  [dim]{work_path}[/dim]")

        if skip_build:
            console.print(
                "  [yellow]⚠[/yellow]  [dim]--skip-build: build phase will be skipped[/dim]"
            )

        console.rule(style="dim")
        console.print(f"\n  Running agent  [dim]{repo_path}[/dim]\n", highlight=False)

        rc = run_container(repo_path, work_path, config_path, debug_path, skip_build)
        if rc != 0:
            console.print(
                f"\n  [red]✗[/red] [bold red]Container execution failed "
                f"(exit code {rc}).[/bold red]\n"
            )
            sys.exit(rc)

        results_path = work_path / "agent_results.json"
        completion_text = Text.assemble(
            ("AUDIT ENGINE PIPELINE COMPLETE\n\n", "bold cyan"),
            ("Status:     ", "dim"),
            ("Active / Success\n", "bold"),
            ("Artifacts:  ", "dim"),
            (f"{results_path.name}\n", "cyan"),
            ("Location:   ", "dim"),
            (f"{work_path}", "italic dim"),
        )
        console.print(
            Panel(
                completion_text,
                box=box.ROUNDED,
                border_style="cyan",
                padding=(1, 2),
                expand=False,
            ),
            highlight=False,
        )
        console.print()

    except KeyboardInterrupt:
        handle_abort()


if __name__ == "__main__":
    main()
