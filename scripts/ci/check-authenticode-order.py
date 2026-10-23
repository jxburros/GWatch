#!/usr/bin/env python3
"""Checks the order of the Authenticode steps in the release workflow (#120).

    python3 scripts/ci/check-authenticode-order.py .github/workflows/release.yml

Authenticode signing rewrites the .exe files, so everything that describes those
bytes has to come after it: the setup programs must package the signed
executables, the .sha256 files and the winget manifest's hash must be computed
over the signed files, and the ed25519 .sig files (made by a later job) must
sign them. This reads the workflow and fails if any of that is out of order, if
a signing step would run when signing is not configured, or if the
verification that makes a signed release fail closed has gone missing.
"""
import re
import sys

import yaml

CONFIGURED = "steps.authenticode.outputs.enabled == 'true'"

# job -> (files signed before ISCC, the step that hashes the result, the job
# that adds the ed25519 signatures)
JOBS = {
    "gwatch-build": ("Authenticode-sign the Windows executables", "Checksum the final bytes", "gwatch-sign"),
    "gwatch-agent-build": ("Authenticode-sign the Windows executables", "Checksum the final bytes", "gwatch-agent-sign"),
    "agent-winget": ("Authenticode-sign the packaged agent", "Render the manifests", None),
}


def main(path):
    wf = yaml.safe_load(open(path, encoding="utf-8"))
    jobs = wf["jobs"]
    errors = []

    for name, (sign_step, hash_step, sig_job) in JOBS.items():
        job = jobs.get(name)
        if job is None:
            errors.append(f"{name}: job is missing")
            continue
        steps = job.get("steps", [])
        names = [s.get("name", s.get("uses", "")) for s in steps]

        def index(step_name):
            if step_name not in names:
                errors.append(f"{name}: no step named {step_name!r}")
                return None
            return names.index(step_name)

        configure = index("Authenticode configuration")
        install = index("Install the Authenticode signing client")
        sign = index(sign_step)
        verify = index("Verify the Authenticode signatures")
        hashed = index(hash_step)
        login = next((i for i, s in enumerate(steps) if str(s.get("uses", "")).startswith("azure/login@")), None)
        iscc = [i for i, s in enumerate(steps) if "& $iscc" in str(s.get("run", ""))]
        if login is None:
            errors.append(f"{name}: no azure/login step")
        elif not re.fullmatch(r"azure/login@[0-9a-f]{40}", steps[login]["uses"]):
            errors.append(f"{name}: azure/login must be pinned to a full commit SHA")
        if len(iscc) != 1:
            errors.append(f"{name}: expected exactly one step that runs ISCC, found {len(iscc)}")
        if None in (configure, install, sign, verify, hashed, login) or len(iscc) != 1:
            continue

        order = [("configuration", configure), ("azure/login", login), ("install", install),
                 ("sign", sign), ("ISCC", iscc[0]), ("verify", verify), ("hash", hashed)]
        for (a, ia), (b, ib) in zip(order, order[1:]):
            if not ia < ib:
                errors.append(f"{name}: the {a} step must come before the {b} step")

        for i in (login, install, sign, verify):
            if steps[i].get("if") != CONFIGURED:
                errors.append(f"{name}: step {names[i]!r} must run only `if: {CONFIGURED}`")
        if "if" in steps[iscc[0]]:
            errors.append(f"{name}: the ISCC step must run signed or not")
        if "-Action IsccArgs" not in steps[iscc[0]]["run"] or "@signArgs" not in steps[iscc[0]]["run"]:
            errors.append(f"{name}: the ISCC step must pass the IsccArgs from scripts/authenticode.ps1, or the setup program goes out unsigned")

        if job.get("permissions", {}).get("id-token") != "write":
            errors.append(f"{name}: needs `permissions: id-token: write` for azure/login's OIDC sign-in")

        # Nothing before the signing step may write checksums of the binaries.
        for s in steps[:sign]:
            if "sha256sum" in str(s.get("run", "")):
                errors.append(f"{name}: step {s.get('name')!r} hashes files before they are Authenticode-signed")

        if sig_job:
            needs = jobs.get(sig_job, {}).get("needs", [])
            if name not in needs:
                errors.append(f"{sig_job}: must need {name}, so the ed25519 signatures cover the Authenticode-signed files")

    for e in errors:
        print(f"::error file={path}::{e}")
    if errors:
        return 1
    print("release workflow: Authenticode signing comes before packaging, hashing and the ed25519 signatures")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else ".github/workflows/release.yml"))
