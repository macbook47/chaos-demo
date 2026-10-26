#!/usr/bin/env python3
"""
Patch krkn-lib (tested against 7.0.3) so pod_disruption_scenarios work for
users who only have permissions inside their own namespace.

Problem: when namespace_pattern + label_selector is used, krkn-lib lists pods
across ALL namespaces (list_pod_for_all_namespaces) and then filters by regex.
A namespace-scoped user gets 403 before any pod is killed.

Fix: if namespace_pattern is a plain namespace name (optionally wrapped in
^...$, e.g. "macbook47-dev" or "^qa-nds$"), use the namespaced API
(list_namespaced_pod) instead. Real regex patterns ("nds-.*") keep the
original cluster-wide behaviour, so nothing changes for admin users.

Patched call sites:
  1. pod_monitor.select_and_monitor_by_namespace_pattern_and_label
     (initial pod snapshot + the recovery watch)
  2. KrknKubernetes.select_pods_by_namespace_pattern_and_label
     (pod selection for the kill, and wait_for_pods after the kill)

Usage: python3 patch_krkn_lib.py <pod_monitor.py> <krkn_kubernetes.py>
The script refuses to patch if the expected code is not found, so a different
krkn-lib version fails loudly instead of being silently mis-patched.
"""
import sys

HELPER = '''

# --- namespace-scoped patch (added for non-admin users) ---
_DNS_LABEL_RE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")


def _literal_namespace(namespace_pattern):
    """Return the namespace name if the pattern is a plain name
    (optionally ^anchored$ and/or re.escape()d), else None."""
    if not namespace_pattern:
        return None
    s = namespace_pattern
    if s.startswith("^"):
        s = s[1:]
    if s.endswith("$"):
        s = s[:-1]
    s = s.replace("\\\\-", "-")
    return s if _DNS_LABEL_RE.match(s) else None
# --- end patch ---
'''


def replace_once(text, old, new, label):
    count = text.count(old)
    if count != 1:
        sys.exit(f"[patch] {label}: expected 1 match, found {count}. "
                 "Different krkn-lib version? Aborting, nothing written.")
    return text.replace(old, new)


def patch_pod_monitor(path):
    src = open(path).read()
    if "_literal_namespace" in src:
        print(f"[patch] {path}: already patched, skipping")
        return

    # helper after the imports
    src = replace_once(
        src,
        "from kubernetes.client.exceptions import ApiException\n",
        "from kubernetes.client.exceptions import ApiException\n" + HELPER,
        "pod_monitor imports",
    )

    old = '''    select_partial = partial(
        v1_client.list_pod_for_all_namespaces,
        label_selector=label_selector,
        field_selector="status.phase=Running",
    )
    snapshot = _select_pods(
        select_partial,
        namespace_pattern=namespace_pattern,
    )

    monitor_partial = partial(
        v1_client.list_pod_for_all_namespaces,
        resource_version=snapshot.resource_version,
        label_selector=label_selector,
    )
'''
    new = '''    literal_ns = _literal_namespace(namespace_pattern)
    if literal_ns:
        logging.info(
            f"namespace-scoped mode: monitoring pods only in '{literal_ns}'"
        )
        list_fn = partial(v1_client.list_namespaced_pod, literal_ns)
    else:
        list_fn = v1_client.list_pod_for_all_namespaces

    select_partial = partial(
        list_fn,
        label_selector=label_selector,
        field_selector="status.phase=Running",
    )
    snapshot = _select_pods(
        select_partial,
        namespace_pattern=namespace_pattern,
    )

    monitor_partial = partial(
        list_fn,
        resource_version=snapshot.resource_version,
        label_selector=label_selector,
    )
'''
    src = replace_once(src, old, new, "select_and_monitor_by_namespace_pattern_and_label")
    open(path, "w").write(src)
    print(f"[patch] {path}: patched")


def patch_krkn_kubernetes(path):
    src = open(path).read()
    if "_literal_namespace" in src:
        print(f"[patch] {path}: already patched, skipping")
        return

    src = replace_once(
        src,
        "from kubernetes.client.rest import ApiException\n",
        "from kubernetes.client.rest import ApiException\n" + HELPER,
        "krkn_kubernetes imports",
    )

    old = '''        namespace_re = re.compile(namespace_pattern)
        pods_and_namespaces = self.get_all_pods(label_selector, field_selector)
'''
    new = '''        namespace_re = re.compile(namespace_pattern)
        literal_ns = _literal_namespace(namespace_pattern)
        if literal_ns:
            ret = self.cli.list_namespaced_pod(
                literal_ns,
                label_selector=label_selector,
                field_selector=field_selector,
            )
            return [(p.metadata.name, literal_ns) for p in ret.items]
        pods_and_namespaces = self.get_all_pods(label_selector, field_selector)
'''
    src = replace_once(src, old, new, "select_pods_by_namespace_pattern_and_label")
    open(path, "w").write(src)
    print(f"[patch] {path}: patched")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    patch_pod_monitor(sys.argv[1])
    patch_krkn_kubernetes(sys.argv[2])
    print("[patch] done")
