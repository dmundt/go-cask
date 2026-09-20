"""mkdocs-macros-plugin hook module.

Exposes build-time environment variables as Jinja macros so that
website/impressum.md can render legally required content injected via
the IMPRESSUM secret without hardcoding it in the repository.
"""

import os


def define_env(env):
    """Register variables available to `{{ ... }}` expressions in Markdown."""
    env.variables["IMPRESSUM"] = os.environ.get("IMPRESSUM", "")
