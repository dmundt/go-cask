"""mkdocs-macros-plugin hook module.

Exposes build-time environment variables as Jinja macros so that
website/impressum.md can render legally required content injected via
the IMPRESSUM secret without hardcoding it in the repository.
"""

import os


def define_env(env):
    """Register variables available to `{{ ... }}` expressions in Markdown."""
    impressum = os.environ.get("IMPRESSUM", "")
    if not impressum.strip():
        raise RuntimeError("IMPRESSUM environment variable must not be empty")
    env.variables["IMPRESSUM"] = impressum
