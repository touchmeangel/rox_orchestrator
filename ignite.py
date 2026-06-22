import json
import os
import platform
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
        "detect": lambda p: (
            (p / "foundry.toml").exists() or (p / "forge.toml").exists()
        ),
        "image": "touchmeangel/ignite_agent:latest",
    }
}

PROVIDERS = {
    "anthropic": {
        "label": "Anthropic",
        "models": [
            "claude-opus-4-8",
            "claude-opus-4-7",
            "claude-sonnet-4-6",
            "claude-opus-4-6",
            "claude-haiku-4.5",
        ],
        "env_key": "ANTHROPIC_API_KEY",
        "provider_str": "anthropic",
        "base_url": None,
    },
    "openai": {
        "label": "OpenAI",
        "models": [
            "gpt-5.5",
            "gpt-5",
            "gpt-4.1-mini",
            "gpt-4o",
        ],
        "env_key": "OPENAI_API_KEY",
        "provider_str": "openai",
        "base_url": None,
    },
    "google": {
        "label": "Google",
        "models": [
            "gemini-3.5-flash",
            "gemini-3.1-pro",
            "gemini-3.1-flash-lite",
            "gemini-2.5-flash",
        ],
        "env_key": "GOOGLE_API_KEY",
        "provider_str": "openai",
        "base_url": "https://generativelanguage.googleapis.com/v1beta/openai/",
    },
    "ollama": {
        "label": "Ollama (Local Engine via OpenAI implementation)",
        "models": [],
        "env_key": "OLLAMA_API_KEY",
        "provider_str": "openai",
        "base_url": "http://localhost:11434/v1",
    },
    "custom_openai_compatible": {
        "label": "Custom (OpenAI Compatible Gateway)",
        "models": [],
        "env_key": None,
        "provider_str": "openai",
        "base_url": "http://localhost:8000/v1",
    },
}

FALLBACK_OLLAMA_MODELS = []

CUSTOM_MODEL_LABEL = "[ enter custom model id ]"


def handle_abort():
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
    return key


CONFIRMED_PATHS_FILE = IGNITE_HOME / "confirmed_paths.json"


def _load_confirmed_paths() -> set[str]:
    if CONFIRMED_PATHS_FILE.exists():
        try:
            return set(json.loads(CONFIRMED_PATHS_FILE.read_text()))
        except Exception:
            return set()
    return set()


def _save_confirmed_paths(paths: set[str]) -> None:
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)
    CONFIRMED_PATHS_FILE.write_text(json.dumps(sorted(paths), indent=2))


def confirm_folder_access(project_path: str, auto_confirm: bool = False) -> None:
    confirmed = _load_confirmed_paths()
    if project_path in confirmed:
        return

    warning_text = (
        f"[bold red]AGENT FILE SYSTEM ACCESS[/bold red]\n\n"
        f"You are about to run an autonomous agent on: [cyan]{project_path}[/cyan]\n"
        f"The agent will have full permissions to [bold]read, write, and execute tools[/bold] "
        f"inside this directory.\n\n"
        f"[dim]Ensure you have committed any sensitive local changes to git before proceeding.[/dim]"
    )
    console.print(Panel(warning_text, border_style="red", padding=(1, 2)))

    if auto_confirm:
        console.print(
            f"  [yellow]⚠[/yellow]  [bold]-y flag set[/bold] — auto-accepting risk for [cyan]{project_path}[/cyan]\n"
        )
        confirmed.add(project_path)
        _save_confirmed_paths(confirmed)
        return

    granted = questionary.confirm(
        f"Grant the agent write permissions to {project_path}?",
        default=False,
        style=Q_STYLE,
    ).ask()

    if not granted:
        console.print("\n  [dim]⚠  Execution aborted by user. Safe choice![/dim]\n")
        sys.exit(0)

    confirmed.add(project_path)
    _save_confirmed_paths(confirmed)


_BACK = "← Go back"


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


def _ask_reconfigure_target(config_path: Path) -> str | None:
    try:
        cfg = json.loads(config_path.read_text())
    except Exception:
        return "both"

    f_model = cfg.get("models", {}).get("flash", {}).get("model", "?")
    r_model = cfg.get("models", {}).get("reasoning", {}).get("model", "?")

    choice = questionary.select(
        "What would you like to reconfigure?",
        choices=[
            f"Flash model  (current: {f_model})",
            f"Reasoning model  (current: {r_model})",
            "Both models",
            "← Cancel",
        ],
        style=Q_STYLE,
    ).ask()

    if choice is None or choice == "← Cancel":
        return None
    if choice.startswith("Flash"):
        return "flash"
    if choice.startswith("Reasoning"):
        return "reasoning"
    return "both"


def _ask_model_profile(
    role_name: str,
    allow_back: bool = False,
) -> tuple[str, str, str | None, str | None] | None:
    while True:  # inner loop: back-from-model re-shows provider select
        provider_choices = [p["label"] for p in PROVIDERS.values()]
        if allow_back:
            provider_choices.append(_BACK)

        provider_label = questionary.select(
            f"Select provider for [{role_name.upper()}] engine:",
            choices=provider_choices,
            style=Q_STYLE,
        ).ask()
        if provider_label is None:
            handle_abort()
        if provider_label == _BACK:
            return None

        prov_key = next(k for k, v in PROVIDERS.items() if v["label"] == provider_label)
        prov = PROVIDERS[prov_key]

        if prov_key == "ollama":
            base_url = questionary.text(
                "Ollama endpoint address:", default=prov["base_url"], style=Q_STYLE
            ).ask()
            if base_url is None:
                handle_abort()
            base_url = base_url or prov["base_url"]

            container_url = base_url
            if "localhost" in base_url or "127.0.0.1" in base_url:
                container_url = base_url.replace(
                    "localhost", "host.docker.internal"
                ).replace("127.0.0.1", "host.docker.internal")

            models = get_ollama_models(base_url)
            model = questionary.select(
                "Select model tag:",
                choices=(models or FALLBACK_OLLAMA_MODELS) + [CUSTOM_MODEL_LABEL, _BACK],
                style=Q_STYLE,
            ).ask()
            if model is None:
                handle_abort()
            if model == _BACK:
                continue

            if "  [dim]" in model:
                model = model.split("  ")[0]

            if model == CUSTOM_MODEL_LABEL:
                model = questionary.text(
                    "Model tag (e.g. llama3.1:8b):", style=Q_STYLE
                ).ask()
                if model is None:
                    handle_abort()
                model = model or (FALLBACK_OLLAMA_MODELS[0] if FALLBACK_OLLAMA_MODELS else "")

            return prov["provider_str"], model, container_url, prov["env_key"]

        if prov_key == "custom_openai_compatible":
            base_url = questionary.text(
                "Target API Base URL endpoint:", default=prov["base_url"], style=Q_STYLE
            ).ask()
            if base_url is None:
                handle_abort()
            base_url = base_url or prov["base_url"]

            model = questionary.text(
                "Target model id (e.g. openrouter/auto, deepseek-chat):", style=Q_STYLE
            ).ask()
            if model is None:
                handle_abort()

            return prov["provider_str"], model, base_url, f"{role_name.upper()}_CUSTOM_API_KEY"

        model = questionary.select(
            "Select a model variant:",
            choices=prov["models"] + [CUSTOM_MODEL_LABEL, _BACK],
            style=Q_STYLE,
        ).ask()
        if model is None:
            handle_abort()
        if model == _BACK:
            continue

        if model == CUSTOM_MODEL_LABEL:
            model = questionary.text("Model signature string:", style=Q_STYLE).ask()
            if not model:
                handle_abort()

        return prov["provider_str"], model, prov.get("base_url"), prov.get("env_key")


def run_setup(manager: str, reconfigure_target: str = "both") -> dict:
    """
    reconfigure_target: "both" | "flash" | "reasoning"
    """
    config_path = IGNITE_HOME / "config.json"
    env_path = IGNITE_HOME / ".env"
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)

    existing: dict = {}
    if config_path.exists():
        try:
            existing = json.loads(config_path.read_text())
        except Exception:
            pass

    configure_flash = reconfigure_target in ("both", "flash")
    configure_reasoning = reconfigure_target in ("both", "reasoning")
    step = 1

    if configure_flash:
        console.print()
        console.rule(f"[dim]STEP {step} — Flash Model (Fast routing tasks, sub-scans)[/dim]", style="dim")
        step += 1
        result = _ask_model_profile("flash", allow_back=False)
        f_provider, f_model, f_base_url, f_env_key = result  # result is never None here
    else:
        fc = existing.get("models", {}).get("flash", {})
        f_provider, f_model = fc.get("provider", "openai"), fc.get("model", "")
        f_base_url, f_env_key = fc.get("base_url"), fc.get("api_key_env")

    if configure_reasoning:
        console.print()
        console.rule(f"[dim]STEP {step} — Reasoning Model (Deep analysis, logic reviews)[/dim]", style="dim")
        step += 1

        while True:
            result = _ask_model_profile("reasoning", allow_back=configure_flash)
            if result is not None:
                r_provider, r_model, r_base_url, r_env_key = result
                break
            console.print()
            console.rule("[dim]STEP 1 — Flash Model (Fast routing tasks, sub-scans)[/dim]", style="dim")
            flash_result = _ask_model_profile("flash", allow_back=False)
            f_provider, f_model, f_base_url, f_env_key = flash_result
            console.print()
            console.rule("[dim]STEP 2 — Reasoning Model (Deep analysis, logic reviews)[/dim]", style="dim")
    else:
        rc = existing.get("models", {}).get("reasoning", {})
        r_provider, r_model = rc.get("provider", "openai"), rc.get("model", "")
        r_base_url, r_env_key = rc.get("base_url"), rc.get("api_key_env")

    console.print()
    console.rule(f"[dim]STEP {step} — Security Credentials & Token Storage[/dim]", style="dim")

    stored_keys = _load_env_file()
    collected_keys = dict(stored_keys)

    keys_to_handle: dict[str, str | None] = {}
    if configure_flash and f_env_key:
        keys_to_handle[f_env_key] = "flash"
    if configure_reasoning and r_env_key and r_env_key not in keys_to_handle:
        keys_to_handle[r_env_key] = "reasoning"

    for env_key, _role in keys_to_handle.items():
        if env_key == "OLLAMA_API_KEY":
            collected_keys[env_key] = "ollama"
            continue

        if stored_keys.get(env_key):
            keep = questionary.confirm(
                f"Keep existing {env_key}?", default=True, style=Q_STYLE
            ).ask()
            if keep is None:
                handle_abort()
            if keep:
                continue

        token = _ask_api_key(env_key)
        if token:
            collected_keys[env_key] = token

    def _build_entry(provider, model, base_url, env_key, temperature, max_tokens) -> dict:
        entry: dict = {"provider": provider, "model": model,
                       "temperature": temperature, "max_tokens": max_tokens}
        if base_url:
            entry["base_url"] = base_url
        if env_key:
            entry["api_key_env"] = env_key
        return entry

    flash_entry = (
        _build_entry(f_provider, f_model, f_base_url, f_env_key, 0.1, 4096)
        if configure_flash
        else existing.get("models", {}).get("flash", {})
    )
    reasoning_entry = (
        _build_entry(r_provider, r_model, r_base_url, r_env_key, 0.3, 8192)
        if configure_reasoning
        else existing.get("models", {}).get("reasoning", {})
    )

    config = {
        "models": {"flash": flash_entry, "reasoning": reasoning_entry},
        "task_routing": existing.get("task_routing", {"default": "flash", "reasoning": "reasoning"}),
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
        "FLASH_CUSTOM_API_KEY",
        "REASONING_CUSTOM_API_KEY",
    )
    for key in target_keys:
        if key in os.environ and key not in env:
            env[key] = os.environ[key]
    return env


def pull_image(manager: str):
    docker_path = shutil.which("docker") or "docker"
    image = MANAGERS[manager]["image"]
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
    manager: str, project_path: Path, config_path: Path, debug_path: Path
) -> int:
    _ensure_debug_file(debug_path)

    docker_path = shutil.which("docker") or "docker"
    image = MANAGERS[manager]["image"]
    env_vars = load_env_vars()

    extra_hosts = (
        ["--add-host", "host.docker.internal:host-gateway"]
        if platform.system() == "Linux"
        else []
    )

    env_flags = []
    for k, v in env_vars.items():
        env_flags += ["-e", f"{k}={v}"]

    user_mapping = []
    if platform.system() != "Windows":
        user_mapping = ["--user", f"{os.getuid()}:{os.getgid()}"]

    cmd = [
        docker_path,
        "run",
        "--rm",
        "-t",
        "-v",
        f"{project_path}:/project",
        "-v",
        f"{config_path}:/app/config.json:ro",
        "-v",
        f"{debug_path}:/app/debug.log:rw",
        *user_mapping,
        "-e",
        "PROJECT_PATH=/project",
        *env_flags,
        *extra_hosts,
        image,
        "--project-path",
        "/project",
        "--output",
        "/project/agent_results.json",
        "--debug",
        "/app/debug.log",
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


def usage():
    console.print(
        Panel(
            "[bold]ignite[/bold] — EVM security research agent (https://github.com/touchmeangel/ignite_agent)\n\n"
            "  [cyan]ignite[/cyan]                             [dim](Auto-detect manager in current directory)[/dim]\n"
            "  [cyan]ignite -u[/cyan]                          [dim](Pull latest image and run in current directory)[/dim]\n"
            "  ignite foundry .                   [dim](Explicit manager and path)[/dim]\n"
            "  ignite /path/to/project            [dim](Explicit path, auto-detect manager)[/dim]\n"
            "  ignite /path/to/project -y         [dim](Allow access to new folder without manual input)[/dim]\n"
            "  ignite . [dim]-r or --reconfigure[/dim]\n"
            "  ignite [dim]-h or --help[/dim]                      [dim](Show this help utility view)[/dim]\n\n"
            f"  Available managers: {', '.join(MANAGERS)}",
            title="Usage",
            box=box.ROUNDED,
        )
    )


def main():
    try:
        args = sys.argv[1:]
        flags = {a for a in args if a.startswith("-")}
        positional = [a for a in args if not a.startswith("-")]

        if "--help" in flags or "-h" in flags:
            usage()
            sys.exit(0)

        reconfigure = "--reconfigure" in flags or "-r" in flags
        do_update = "--update" in flags or "-u" in flags
        allow_folder_access = "-y" in flags

        manager = None
        project_path = Path(".").resolve()

        if len(positional) == 2:
            manager = positional[0].lower()
            project_path = Path(positional[1]).resolve()
        elif len(positional) == 1:
            val = positional[0]
            if val.lower() in MANAGERS:
                manager = val.lower()
            else:
                project_path = Path(val).resolve()
        elif len(positional) > 2:
            usage()
            sys.exit(1)

        if not project_path.exists():
            console.print(
                f"  [red]✗[/red]  Path not found: {project_path}", highlight=False
            )
            sys.exit(1)

        if manager is None:
            detected = [
                m_name
                for m_name, m_def in MANAGERS.items()
                if m_def["detect"](project_path)
            ]

            if len(detected) == 1:
                manager = detected[0]
            elif len(detected) > 1:
                console.print(
                    f"  [red]✗[/red]  Conflict: Multiple managers detected ({', '.join(detected)})."
                )
                console.print(
                    "      Please specify your target manager explicitly (e.g., ignite foundry .)"
                )
                sys.exit(1)
            else:
                console.print(
                    f"  [red]✗[/red]  Could not auto-detect an EVM project setup at [dim]{project_path}[/dim]"
                )
                console.print(f"      Available options: {', '.join(MANAGERS)}")
                sys.exit(1)

        if manager not in MANAGERS:
            console.print(
                f"  [red]✗[/red]  Unknown manager '{manager}'. "
                f"Available: {', '.join(MANAGERS)}"
            )
            sys.exit(1)

        debug_path = IGNITE_HOME / "debug.log"
        console.print(f"  [dim]➔ Debug logging on[/dim]: [cyan]{debug_path}[/cyan]")
        console.print(f"  [dim]➔ Detected manager:[/dim] [cyan]{manager}[/cyan]")

        config_path = IGNITE_HOME / "config.json"
        console.print()

        if config_path.exists() and not reconfigure:
            cfg = json.loads(config_path.read_text())
            r_model = cfg.get("models", {}).get("reasoning", {}).get("model", "?")
            console.print(
                f"  [cyan]✔[/cyan]  Config found  [dim](reasoning: {r_model})[/dim]"
            )
        else:
            reconfigure_target = "both"
            if reconfigure:
                if config_path.exists():
                    reconfigure_target = _ask_reconfigure_target(config_path)
                    if reconfigure_target is None:
                        console.print("\n  [dim]Reconfiguration cancelled.[/dim]\n")
                        sys.exit(0)
                    console.print(f"  [dim]Reconfiguring {reconfigure_target} model(s) …[/dim]")
                else:
                    console.print("  [dim]No existing config — running full setup.[/dim]")
            else:
                console.print("  [dim]No config found.[/dim]")

            mdef = MANAGERS[manager]
            console.print(f"  [cyan]✔[/cyan]  Using {mdef['label']} framework pipeline")

            if not docker_running():
                console.print(
                    "  [red]✗[/red]  Docker is not running. Start Docker Desktop and retry."
                )
                sys.exit(1)
            console.print("  [cyan]✔[/cyan]  Docker available")

            run_setup(manager, reconfigure_target=reconfigure_target)

        if do_update:
            pull_image(manager)

        confirm_folder_access(str(project_path), auto_confirm=allow_folder_access)

        console.rule(style="dim")

        console.print(
            f"\n  Running agent  [dim]{project_path}[/dim]\n", highlight=False
        )

        rc = run_container(manager, project_path, config_path, debug_path)
        if rc != 0:
            console.print(
                f"\n  [red]✗[/red] [bold red]Container execution failed (exit code {rc}).[/bold red]\n"
            )
            sys.exit(rc)

        results_path = project_path / "agent_results.json"

        completion_text = Text.assemble(
            ("AUDIT ENGINE PIPELINE COMPLETE\n\n", "bold cyan"),
            ("Status:     ", "dim"),
            ("Active / Success\n", "bold"),
            ("Artifacts:  ", "dim"),
            (f"{results_path.name}\n", "cyan"),
            ("Location:   ", "dim"),
            (f"{project_path}", "italic dim"),
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
