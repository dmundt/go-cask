"""mkdocs-macros-plugin hook module.

Exposes build-time environment variables as Jinja macros so that
website/impressum.md can render legally required content injected via
the IMPRESSUM secret without hardcoding it in the repository.

The module also resolves the site's build provenance and publishes it as
`extra` values for the footer partial
(website/overrides/partials/copyright.html):

- `SITE_BUILD_DATE` environment variable — the deployed revision's
  committer date, ISO 8601. Rendered as the `YYYY-MM-DD` date that
  timestamp names, with no zone conversion and no zone label: at day
  granularity no zone is more correct than the offset the commit carries,
  and a zone would only have to be named if a time were shown.
- `SITE_REVISION` environment variable — the deployed revision, short form.

Determinism comes from the source, not from the zone. `.github/workflows/
website.yml` supplies the deployed commit's own timestamp (the offset is
recorded inside the commit object), so every machine reads the same date
out of one revision and two builds of that revision produce byte-identical
footers — with or without any normalization.

Neither is required: when one is absent the module falls back to the
checkout's own `git log -1 --format=%cI` / `git rev-parse --short HEAD`, and
when that is unavailable too (no repository, no git, bare tarball) the value
is left empty and the footer omits it instead of raising. `mkdocs serve` and
`mkdocs build --strict` therefore succeed with no new environment variable
set. `IMPRESSUM` keeps its stricter contract: it is legally required content,
so an empty value is still an error.
"""

import datetime
import os
import subprocess


def _parse_date(raw):
    """Return the `datetime.date` an ISO 8601 timestamp names, or None.

    The date is taken exactly as written, offset and all. A timestamp
    without an offset is still accepted; it needs no zone assumption here
    because reading its date does not depend on one. Nothing is converted
    between zones, so a commit made at `00:30 +02:00` on the 24th reports
    the 24th, the same day the commit page and any changelog entry show.
    """
    value = raw.strip()
    if not value:
        return None
    if value.endswith(("Z", "z")):
        value = value[:-1] + "+00:00"
    try:
        parsed = datetime.datetime.fromisoformat(value)
    except ValueError:
        return None
    return parsed.date()


def _git(*args):
    """Return stdout from a read-only git query, or "" when git cannot answer."""
    try:
        completed = subprocess.run(
            ["git", *args],
            capture_output=True,
            check=False,
            text=True,
        )
    except OSError:
        return ""
    return completed.stdout.strip() if completed.returncode == 0 else ""


def _build_date():
    """The deployed revision's date as `YYYY-MM-DD`, or an empty string."""
    parsed = _parse_date(os.environ.get("SITE_BUILD_DATE", ""))
    if parsed is None:
        parsed = _parse_date(_git("log", "-1", "--format=%cI"))
    return parsed.isoformat() if parsed else ""


def _revision():
    """The deployed revision in short form, or an empty string."""
    revision = os.environ.get("SITE_REVISION", "").strip()
    if not revision:
        revision = _git("rev-parse", "--short", "HEAD")
    return revision


def define_env(env):
    """Register variables available to `{{ ... }}` expressions in Markdown."""
    impressum = os.environ.get("IMPRESSUM", "")
    if not impressum.strip():
        raise RuntimeError("IMPRESSUM environment variable must not be empty")
    env.variables["IMPRESSUM"] = impressum

    # Published for the theme templates as well as for the Markdown pages:
    # the footer partial reads `config.extra`, the same mapping.
    extra = env.conf.setdefault("extra", {})
    extra["SITE_BUILD_DATE"] = _build_date()
    extra["SITE_REVISION"] = _revision()
