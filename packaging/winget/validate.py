"""Validates rendered winget manifests against winget's own JSON schemas.

    python3 packaging/winget/validate.py <schema-dir> <manifest-dir>

<schema-dir> holds manifest.{version,installer,defaultLocale}.1.6.0.json from
microsoft/winget-cli (schemas/JSON/manifests/v1.6.0); CI downloads them. This
is the check winget-pkgs' own validation starts with, run before a tag rather
than in somebody else's pull request queue. Needs PyYAML and jsonschema.
"""

import json
import os
import sys

import jsonschema
import yaml

KINDS = {
    "GWatch.Agent.yaml": "version",
    "GWatch.Agent.installer.yaml": "installer",
    "GWatch.Agent.locale.en-US.yaml": "defaultLocale",
}


def main(schema_dir, manifest_dir):
    failed = False
    for name, kind in KINDS.items():
        with open(os.path.join(schema_dir, f"manifest.{kind}.1.6.0.json"), encoding="utf-8") as f:
            schema = json.load(f)
        with open(os.path.join(manifest_dir, name), encoding="utf-8") as f:
            # winget reads every scalar as a string where the schema says so;
            # BaseLoader keeps "1.6.0" and dates from turning into numbers.
            manifest = yaml.load(f, Loader=yaml.BaseLoader)
        errors = sorted(jsonschema.Draft7Validator(schema).iter_errors(manifest), key=lambda e: list(e.path))
        for e in errors:
            failed = True
            print(f"{name}: {'/'.join(map(str, e.path)) or '(root)'}: {e.message}")
        if not errors:
            print(f"ok {name}")
    return 1 if failed else 0


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(2)
    sys.exit(main(sys.argv[1], sys.argv[2]))
