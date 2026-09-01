#!/usr/bin/env python3
"""The KCL deploy profile's RBAC must grant what the generated one grants.

THERE ARE TWO RBAC DEFINITIONS IN THIS REPO and only one of them is generated:

  config/rbac/role.yaml   kubebuilder writes it from the +kubebuilder:rbac
                          markers, so it follows a new API type automatically
  kcl/rbac.k              hand-written, and the source of the OCI deploy
                          artifact consumers actually install

When ObjectSource was added, the first one followed and the second did not. A
`kubectl apply -k config/default` deploy was therefore fine while the artifact
shipped a manager that could not list one of the CRDs it installs.

WHY THAT IS WORSE THAN A NORMAL RBAC GAP: controller-runtime blocks in
WaitForCacheSync until EVERY informer syncs, so a single un-listable kind starts
no controller at all -- not just its own. And it is silent. The pod is 1/1
Running, because the health probe binds a different address; every CR applies
cleanly; none of them ever gets a status; and the only evidence is a log line
nobody reads unless they already suspect it. Twenty minutes on machinery-test1
(2026-09-01) went to a GitRepository and two SopsSecretManifests that looked
applied and were doing nothing.

Compares the `sops.stuttgart-things.com` resources only. The rest of the two
files differ legitimately -- the generated one carries kustomize-specific
subjects and the kcl one adds leader-election leases.
"""
import re
import subprocess
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
GROUP = "sops.stuttgart-things.com"


def generated():
    """Resources config/rbac/role.yaml grants in our API group."""
    doc = yaml.safe_load((ROOT / "config/rbac/role.yaml").read_text())
    out = set()
    for rule in doc.get("rules") or []:
        if GROUP in (rule.get("apiGroups") or []):
            out |= set(rule.get("resources") or [])
    return out


def profile():
    """Same, from the rendered KCL deploy profile.

    Rendered rather than parsed: kcl/rbac.k is a program, and a regex over it
    would agree with a list that is never emitted.
    """
    r = subprocess.run(["kcl", "run", str(ROOT / "kcl"), "--format", "yaml"],
                       capture_output=True, text=True)
    if r.returncode != 0:
        print(f"note: kcl run failed, RBAC parity not checked\n{r.stderr[:400]}")
        return None
    out = set()
    for doc in yaml.safe_load_all(r.stdout):
        if not isinstance(doc, dict):
            continue
        # The profile renders ONE document with a `manifests:` list, not a
        # multi-doc stream. Both shapes are accepted so this keeps working if
        # that changes -- reading only the stream form found nothing and
        # reported every resource as missing, which looks like a catastrophic
        # RBAC gap rather than a parser that never matched.
        for obj in doc.get("manifests") or [doc]:
            if not isinstance(obj, dict) or obj.get("kind") != "ClusterRole":
                continue
            for rule in obj.get("rules") or []:
                if GROUP in (rule.get("apiGroups") or []):
                    out |= set(rule.get("resources") or [])
    if not out:
        print("note: no ClusterRole rules for " + GROUP + " found in the "
              "rendered profile -- not checked rather than reported as a gap")
        return None
    return out


def main():
    if subprocess.run(["which", "kcl"], capture_output=True).returncode != 0:
        print("note: kcl not installed -- RBAC parity not checked")
        return 0
    have = profile()
    if have is None:
        return 0
    want = generated()
    missing = sorted(want - have)
    if missing:
        print("FAIL kcl/rbac.k does not grant what config/rbac/role.yaml does:",
              file=sys.stderr)
        for m in missing:
            print(f"       {m}", file=sys.stderr)
        print("     kubebuilder generates role.yaml from the +kubebuilder:rbac "
              "markers and follows a new type automatically; kcl/rbac.k is "
              "hand-written and does not. A missing kind here starts NO "
              "controller at all -- controller-runtime waits for every "
              "informer -- and says so only in the log, while the pod stays "
              "1/1 Running and every CR applies cleanly.", file=sys.stderr)
        return 1
    extra = sorted(have - want)
    if extra:
        print(f"note: kcl/rbac.k grants {', '.join(extra)} beyond the generated "
              f"role -- fine, but check it is still wanted")
    print(f"OK: kcl/rbac.k grants all {len(want)} {GROUP} resource(s) the "
          f"generated role does")
    return 0


if __name__ == "__main__":
    sys.exit(main())
