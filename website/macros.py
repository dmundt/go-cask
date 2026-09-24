"""mkdocs-macros-plugin hook module.

Exposes build-time environment variables as Jinja macros so that
website/impressum.md can render legally required content injected via
the IMPRESSUM secret without hardcoding it in the repository.

The module also completes the site's single footer copyright line: it
reads the checked-out revision with one call — `git log -1 --format=%h
%cs`, the short revision and the commit's own date — and writes the
resulting line back to `env.conf["copyright"]`, the value Material's
footer partial renders. No theme override, no environment variable and no
extra CI step is involved.

The line shows one provenance value: the commit's own date as `YYYY-MM-DD`,
linked to that commit's page, so a reader takes "how current are these docs"
from the visible text and the revision from the link target. It is
revision-derived and never wall-clock (no zone conversion and no zone label —
at day granularity no zone is more correct than the offset the commit
carries), so two builds of one revision produce the same footer, and the `©`
year is derived from that same date instead of a hand-maintained literal.

The line is allowed to degrade: when git cannot answer (no repository, no
git, a tarball build) the provenance fragment and the year are omitted
rather than guessed, and the build does not fail. `IMPRESSUM` keeps its
stricter contract — it is legally required content, so an empty value is
still an error.

Run `python3 website/macros.py --selftest` to check the composition for
fixed inputs, or `--print-copyright` to compose it for this checkout.
"""

import os
import pathlib
import re
import subprocess
import sys

# The `copyright` value in mkdocs.yml is the footer line as it must read
# when no revision can be read. It carries this token, and the hook
# inserts the revision's year directly after it.
COPYRIGHT_YEAR_ANCHOR = "&copy;"

# Provenance is appended to that same line, separated like every other
# element of it, so the footer stays one line.
PROVENANCE_SEPARATOR = "&middot;"

# The one visible provenance value is the commit's date, and this is what it
# links to: the revision belongs in the link target, not in the visible text.
COMMIT_URL = "https://github.com/dmundt/go-cask/commit/"

CONFIG_PATH = pathlib.Path(__file__).resolve().parent.parent / "mkdocs.yml"


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


def revision_provenance():
    """Return `(date, revision)` for the checked-out revision.

    One call answers both: `%h` is the short revision and `%cs` the date
    the commit records as `YYYY-MM-DD`, with no zone conversion. A
    revision whose values cannot be read yields `("", "")` — the caller
    omits them instead of inventing them.
    """
    answer = _git("log", "-1", "--format=%h %cs").split()
    if len(answer) != 2:
        return "", ""
    revision, date = answer
    return date, revision


def compose_copyright(base, date, revision):
    """Return the footer's copyright line for one revision.

    `base` is the `copyright` value mkdocs.yml declares; `date` is the
    deployed revision's date as `YYYY-MM-DD` and `revision` its short
    form, both empty when git could not answer. The year follows
    `COPYRIGHT_YEAR_ANCHOR`, and the line closes with that date as the one
    visible provenance value, linked to the commit it came from; each is
    omitted, never guessed, when its value is missing.
    """
    line = " ".join(base.split())
    if not date:
        return line
    line = line.replace(
        COPYRIGHT_YEAR_ANCHOR, COPYRIGHT_YEAR_ANCHOR + " " + date[:4], 1
    )
    if revision:
        line = '%s %s <a href="%s%s" title="commit %s">%s</a>' % (
            line,
            PROVENANCE_SEPARATOR,
            COMMIT_URL,
            revision,
            revision,
            date,
        )
    return line


_TAG = re.compile(r"<[^>]*>")


def visible_text(line):
    """Return `line` with its tags and their attributes removed.

    The link's href and title carry the revision, so what remains is the text
    a reader sees — the part that must not name the revision.
    """
    return _TAG.sub("", line)


def config_copyright(path=None):
    """Return the `copyright` value from mkdocs.yml, or "" when absent.

    Only the shape mkdocs.yml uses is understood — a folded (`>-`) scalar
    whose continuation lines are indented by two spaces — because this
    runs with the standard library alone, where no YAML parser is
    available. Anything else yields "", which fails the self-test rather
    than silently checking nothing.
    """
    lines = (path or CONFIG_PATH).read_text(encoding="utf-8").splitlines()
    for index, line in enumerate(lines):
        if line.rstrip() != "copyright: >-":
            continue
        parts = []
        for following in lines[index + 1:]:
            if not following.startswith("  ") or following.lstrip().startswith("#"):
                break
            parts.append(following.strip())
        return " ".join(parts)
    return ""


def define_env(env):
    """Register variables available to `{{ ... }}` expressions in Markdown."""
    impressum = os.environ.get("IMPRESSUM", "")
    if not impressum.strip():
        raise RuntimeError("IMPRESSUM environment variable must not be empty")
    env.variables["IMPRESSUM"] = impressum

    # The theme renders `config.copyright` verbatim, so completing that
    # value here reaches the footer of every page without overriding a
    # theme partial.
    date, revision = revision_provenance()
    base = env.conf.get("copyright", "") or ""
    env.conf["copyright"] = compose_copyright(base, date, revision)


def selftest():
    """Check the composed footer line for fixed inputs; return 0 when it holds.

    The checks run with the standard library alone, so `scripts/verify.sh`
    can run them in the gate. They fail when the rendered text changes, a
    zone label or a second line returns, the revision becomes visible text,
    the link stops targeting the revision, the config loses the line, or a
    missing revision is guessed instead of omitted.
    """
    base = config_copyright()
    errors = []

    expected_base = (
        '&copy; Daniel Mundt &middot; <a href="/impressum/">Impressum</a>'
        ' &middot; <a href="/privacy/">Privacy</a> &middot;'
        ' <a href="https://github.com/dmundt/go-cask">GitHub</a>'
    )
    expected_provenance = (
        expected_base.replace("&copy;", "&copy; 2026", 1)
        + ' &middot; <a href="%sab7deab" title="commit ab7deab">2026-09-23</a>'
        % COMMIT_URL
    )

    # (name, date, revision, want) — the want of the first case is the
    # config's own line, so the file that ships is part of the check.
    cases = (
        ("mkdocs.yml copyright", None, None, expected_base),
        ("date and revision", "2026-09-23", "ab7deab", expected_provenance),
        ("neither value", "", "", expected_base),
    )

    for name, date, revision, want in cases:
        if date is None:
            got = base
        else:
            got = compose_copyright(base, date, revision)
        if got != want:
            errors.append("%s: composed %r, want %r" % (name, got, want))
        if "UTC" in got:
            errors.append("%s: the line carries a zone label: %r" % (name, got))
        if "\n" in got or "\r" in got:
            errors.append("%s: the line is not a single line: %r" % (name, got))
        if got.count(COMMIT_URL) > 1:
            errors.append("%s: more than one provenance link: %r" % (name, got))
        if revision:
            if COMMIT_URL + revision not in got:
                errors.append("%s: the link does not target the revision: %r" % (name, got))
            if revision in visible_text(got):
                errors.append("%s: the revision is visible text: %r" % (name, got))
        elif COMMIT_URL in got:
            errors.append("%s: a provenance link without a revision: %r" % (name, got))

    for error in errors:
        print("footer selftest: %s" % error, file=sys.stderr)
    if errors:
        return 1
    print("footer selftest: the copyright line renders as pinned")
    return 0


def main(argv):
    """Run the module's command-line modes; return a process exit status."""
    if argv == ["--selftest"]:
        return selftest()
    if argv == ["--print-copyright"]:
        date, revision = revision_provenance()
        print(compose_copyright(config_copyright(), date, revision))
        return 0
    print(
        "usage: macros.py [--selftest | --print-copyright]",
        file=sys.stderr,
    )
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
