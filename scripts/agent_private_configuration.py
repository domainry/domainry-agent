"""Load explicitly configured local service credentials without printing them."""
import json
import os
from pathlib import Path
import stat
import sys


def load_service_credentials(env):
    directory = Path.home() / ("Library/Application Support/domainry-agent" if sys.platform == "darwin" else ".config/domainry-agent")
    path = Path(env.get("AGENT_WEB_CREDENTIALS_FILE", directory / "web-credentials.json")).expanduser()
    if not path.exists():
        return
    with path.open() as handle:
        info = os.fstat(handle.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise ValueError("Service credential file must be owned by the current user and readable only by that user")
        values = json.load(handle)
    allowed = {"AGENT_PROVIDER_API_KEY", "AGENT_CONVERSATION_MODEL_API_KEY", "AGENT_KNOWLEDGE_API_KEY"}
    if not isinstance(values, dict) or not values or set(values) - allowed:
        raise ValueError("Service credential file contains unsupported keys")
    for name, value in values.items():
        if not isinstance(value, str) or not value.strip() or "\n" in value or "\r" in value:
            raise ValueError("Service credential file contains an invalid value")
        env.setdefault(name, value)
