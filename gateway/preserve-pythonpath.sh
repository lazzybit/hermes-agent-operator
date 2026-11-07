#!/bin/sh
# Rewrite the official Hermes launcher so operator-injected plugin
# dependency directories on PYTHONPATH survive startup. The upstream
# script replaces PYTHONPATH with only the repo and venv site-packages.
set -eu

target="${1:?usage: preserve-pythonpath.sh /opt/hermes/libexec/hermes}"

python=
for candidate in /opt/hermes/.venv/bin/python python3 python; do
	if command -v "$candidate" >/dev/null 2>&1 || [ -x "$candidate" ]; then
		python=$candidate
		break
	fi
done
if [ -z "$python" ]; then
	echo "preserve-pythonpath: no python interpreter found" >&2
	exit 1
fi

"$python" - "$target" << 'PY'
import sys
from pathlib import Path

path = Path(sys.argv[1])
text = path.read_text()
old = (
    "unset PYTHONPATH PYTHONHOME\n"
    'export PYTHONPATH="$REPO:$SITE"\n'
)
new = (
    '_hermes_extra_pythonpath="${PYTHONPATH:-}"\n'
    "unset PYTHONPATH PYTHONHOME\n"
    'if [ -n "$_hermes_extra_pythonpath" ]; then\n'
    '    export PYTHONPATH="$REPO:$SITE:$_hermes_extra_pythonpath"\n'
    "else\n"
    '    export PYTHONPATH="$REPO:$SITE"\n'
    "fi\n"
    "unset _hermes_extra_pythonpath\n"
)
count = text.count(old)
if count != 1:
    sys.exit(
        "preserve-pythonpath: expected exactly one official PYTHONPATH "
        f"stanza in {path}, found {count}"
    )
path.write_text(text.replace(old, new, 1))
print(f"preserve-pythonpath: preserved extra PYTHONPATH in {path}")
PY
