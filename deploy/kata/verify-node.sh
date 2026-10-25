#!/bin/bash
# Root controller inside an explicitly disposable, network=none setup test node.
set -euo pipefail
[[ -f /.dockerenv && ${CODEX_DOCK_DISPOSABLE_NODE:-} == 1 ]]
proof() { echo "VERIFIED: $*"; }
operator() { runuser -u kata-test -- env HOME=/home/kata-test OPENAI_API_KEY=sk-setup-fixture-never-valid "$@"; }
release() { python3 -c 'import json; print(json.load(open("/var/lib/codex-dock-setup/installed.json"))["release"])'; }
cli() { operator /usr/local/bin/oaiso "$@"; }
ctr_node() { "$(release)/bin/ctr" --address /run/codex-dock-containerd/containerd.sock --namespace codex-dock "$@"; }
setup_python=${CODEX_DOCK_SETUP_PYTHON:-/usr/bin/python3}
mkdir -p /evidence /var/cache/codex-dock-setup
for archive in /cache-input/*; do cp "$archive" /var/cache/codex-dock-setup/; done
chmod -R go-w /var/cache/codex-dock-setup
if [[ -n ${CODEX_DOCK_RELEASE_INSTALLER:-} ]]; then
 cat "$CODEX_DOCK_RELEASE_INSTALLER" | bash -s -- --bundle /bundle --operator kata-test --offline > /evidence/plan.txt
else
 /bundle/oaiso setup --python "$setup_python" --bundle /bundle --operator kata-test --offline > /evidence/plan.txt
fi
[[ ! -e /etc/systemd/system/codex-dock-node.service ]]
proof 'planning leaves services uninstalled'
if [[ -n ${CODEX_DOCK_RELEASE_INSTALLER:-} ]]; then
 cat "$CODEX_DOCK_RELEASE_INSTALLER" | bash -s -- --bundle /bundle --operator kata-test --offline --apply > /evidence/setup.txt 2>&1
else
 /bundle/oaiso setup --python "$setup_python" --bundle /bundle --operator kata-test --offline --apply > /evidence/setup.txt 2>&1
fi
cli runtime-check --json > /evidence/readiness.json
initial_release=$(release)
proof 'fresh setup and operator readiness'
# Only this explicitly selected, disposable resource suite defines an I/O
# profile and enables io in its own cgroup namespace. No shared host is changed.
if [[ ${CODEX_DOCK_KATA_ACCEPTANCE_RUN:-} == *ResourceControls* ]]; then
 python3 - <<'PYRESOURCE'
import json, os
from pathlib import Path
# Evidence is a private bind on the host block filesystem, not guest/host tmpfs.
dev = os.stat('/evidence').st_dev
major, minor = os.major(dev), os.minor(dev)
if not major or not Path(f'/sys/dev/block/{major}:{minor}').exists():
    raise SystemExit('real block device required for I/O resource acceptance')
workspace = Path('/evidence/resource-workspaces')
workspace.mkdir(mode=0o700); os.chown(workspace,1000,1000)
profile = dict(name='resource-test',cpu_cores=1,guest_memory_bytes=512<<20,host_memory_bytes=768<<20,pid_limit=256,file_descriptor_limit=512,
               block_io=[dict(major=major,minor=minor,write_bytes_per_second=8<<20,read_bytes_per_second=64<<20,read_iops=4096,write_iops=1024)])
path=Path('/etc/codex-dock/resource-test.json')
path.parent.mkdir(parents=True,exist_ok=True)
path.write_text(json.dumps([profile])); path.chmod(0o600)
# Keep the normal installer-controlled binary path so later upgrades are tested.
base=Path('/etc/systemd/system/codex-dock-node.service').read_text()
line=next(line for line in base.splitlines() if line.startswith('ExecStart='))
line=line.split('ExecStart=',1)[1].split(' node serve ',1)[1]
dropin=Path('/etc/systemd/system/codex-dock-node.service.d/resources.conf')
dropin.parent.mkdir(parents=True,exist_ok=True)
dropin.write_text('[Service]\nExecStart=\nExecStart=/usr/local/bin/oaiso node serve '+line+' --resource-profiles '+str(path)+'\n')
PYRESOURCE
 printf '+io' > /sys/fs/cgroup/cgroup.subtree_control
 printf '+io' > /sys/fs/cgroup/codex-dock/cgroup.subtree_control
 systemctl daemon-reload
 systemctl restart codex-dock-node
 for attempt in $(seq 1 150); do
  if cli profile --node resource-test --json > /evidence/resource-profile.json 2>/evidence/resource-node-start.txt; then break; fi
  sleep .2
 done
 cli profile --node resource-test --json > /evidence/resource-profile.json
fi
if [[ ${CODEX_DOCK_VERIFY_ONBOARDING:-} == 1 ]]; then
 # Test-only network trust/proxy setup for the session's package registry.
 # No host service or credentials are mounted into the disposable node.
 if [[ -r /run/onboarding-ca.crt ]]; then
  install -m 0644 /run/onboarding-ca.crt /usr/local/share/ca-certificates/oaiso-onboarding.crt
  update-ca-certificates > /evidence/onboarding-ca.txt 2>&1
  export CODEX_PROXY_CERT=/usr/local/share/ca-certificates/oaiso-onboarding.crt
 fi
 python3 - <<'PYPROXY'
import json, os
from pathlib import Path
keys = ['HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'no_proxy', 'all_proxy']
lines = [key + '=' + json.dumps(os.environ[key]) for key in keys if os.environ.get(key)]
path = Path('/run/onboarding-build-proxy.env')
path.write_text('\n'.join(lines) + '\n')
path.chmod(0o600)
dropin = Path('/etc/systemd/system/codex-dock-buildkit.service.d/onboarding.conf')
dropin.parent.mkdir(parents=True, exist_ok=True)
dropin.write_text('[Service]\nEnvironmentFile=/run/onboarding-build-proxy.env\n')
PYPROXY
 systemctl daemon-reload
 systemctl restart codex-dock-buildkit
 install -d -o kata-test -g 1000 -m 0700 /evidence/onboarding /home/kata-test/onboarding
 operator python3 /verification/verify-onboarding.py --home-base /home/kata-test/onboarding --evidence /evidence/onboarding > /evidence/onboarding.txt 2>&1
 proof 'fresh keyring login, automatic profile/admin setup, uncached default image and both agent CLIs in real Kata'
fi
ctr_node images import --local /images.tar > /evidence/import.txt
install -d -o kata-test -g 1000 -m 0700 /home/kata-test/.config/codex-dock /home/kata-test/project
proxy_fixture=${CODEX_DOCK_KATA_PROXY_IMAGE:-codex-dock-proxy:policy-verification}
worker_fixture=${CODEX_DOCK_KATA_TEST_IMAGE:-codex-dock:kata-guard}
export CODEX_DOCK_KATA_PROXY_IMAGE=$proxy_fixture CODEX_DOCK_KATA_TEST_IMAGE=$worker_fixture
python3 - <<'PYCONFIG'
import json,os
from pathlib import Path
Path('/home/kata-test/.config/codex-dock/config.toml').write_text('default_image = '+json.dumps(os.environ['CODEX_DOCK_KATA_TEST_IMAGE'])+'\nproxy_image = '+json.dumps(os.environ['CODEX_DOCK_KATA_PROXY_IMAGE'])+'\nauto_build = false\n')
PYCONFIG
chown kata-test:1000 /home/kata-test/.config/codex-dock/config.toml
chmod 0600 /home/kata-test/.config/codex-dock/config.toml
cli init > /evidence/init.txt 2>&1
cli image check "$worker_fixture" --agent codex --json > /evidence/image.json
python3 - <<'PYIMAGE'
import json
from pathlib import Path
checks = json.loads(Path('/evidence/image.json').read_text())['checks']
if not any(check['name'] == 'auth-entrypoint' and check['status'] == 'ok' for check in checks):
    raise SystemExit('test worker entrypoint does not match this CLI; rebuild the worker image before acceptance')
PYIMAGE
proof 'init prepares proxy and validates custom-compatible image without real credentials'
cd /home/kata-test/project
printf host > input
chown kata-test:1000 input
cli run --name setup-worker --detach --keep --no-internet --watch-polling --exec 'python3 -u -c '\''import os,time;from pathlib import Path;assert os.environ["CHOKIDAR_USEPOLLING"]=="true";assert Path("/workspace/input").read_text()=="host";Path("/workspace/created").write_text(f"{os.getuid()}:{os.getgid()}");time.sleep(600)'\''' > /evidence/worker.txt 2>&1
for attempt in $(seq 1 300); do [[ -f created ]] && break; sleep .2; done
if [[ ! -f created ]]; then
 cli logs setup-worker > /evidence/worker-logs.txt 2>&1 || true
 cli ps -a > /evidence/containers-at-timeout.txt 2>&1 || true
 cli vm inspect setup-worker > /evidence/worker-timeout.json 2>&1 || true
 for hosts in /var/lib/codex-dock-node/engine/tasks/*/hosts; do
  [[ -f $hosts ]] || continue
  if grep -q codex-auth-proxy "$hosts"; then
   cp "$hosts" /evidence/worker-hosts-at-timeout.txt
   break
  fi
 done
 {
  ip -brief link
  ip -brief address
  bridge link
  ip netns list
  for namespace in $(ip netns list | awk '{print $1}'); do
   echo "namespace: $namespace"
   ip -n "$namespace" -brief address
   ip -n "$namespace" route
   ip -n "$namespace" neigh
  done
  for interface in /sys/class/net/v*/ifindex; do
   [[ -e $interface ]] || continue
   name=${interface%/ifindex}
   name=${name##*/}
   echo "tc ingress: $name"
   tc filter show dev "$name" ingress
  done
 } > /evidence/network-at-timeout.txt 2>&1 || true
 echo 'worker did not create the shared file within 60 seconds' >&2
 exit 1
fi
[[ $(cat created) == 1000:1000 && $(stat -c '%u:%g' created) == 1000:1000 ]]
cli vm ps -a --json > /evidence/vms-before-refusal.json
if /bundle-next/oaiso setup --python "$setup_python" --bundle /bundle-next --operator kata-test --offline --apply > /evidence/refused-update.txt 2>&1; then
 echo 'update accepted a running worker' >&2; exit 1
fi
[[ $(release) == "$initial_release" ]]
cli vm inspect setup-worker > /evidence/worker-after-refusal.json
cli runtime-check --json > /evidence/readiness-after-refusal.json
proof 'live workspace, numeric ownership, polling environment and refusal to update live resources'
cli rm --force setup-worker
cli proxy stop
cli proxy rm
cli network rm
if [[ -x /acceptance.test ]]; then
 install -d -o kata-test -g 1000 -m 0700 /home/kata-test/owner-fixture
 install -d -o 12001 -g 12002 -m 0777 /home/kata-test/owner-fixture/project
 install -o 12001 -g 12002 -m 0600 /dev/null /home/kata-test/owner-fixture/project/foreign-private
 install -d -o 12001 -g 12002 -m 2777 /home/kata-test/owner-fixture/project/setgid
 install -d -o root -g 1000 -m 0770 /run/setup-node-restart
 operator env CODEX_DOCK_KATA_E2E=1 CODEX_DOCK_KATA_POLICY_E2E=1 CODEX_DOCK_KATA_TEST_BINARY=/usr/local/bin/oaiso \
  CODEX_DOCK_KATA_TEST_IMAGE="$worker_fixture" CODEX_DOCK_KATA_PROXY_IMAGE="$proxy_fixture" \
  CODEX_DOCK_KATA_NETWORK_PROXY_IMAGE="${CODEX_DOCK_KATA_NETWORK_PROXY_IMAGE:-}" \
  CODEX_DOCK_RESOURCE_WORKSPACE=/evidence/resource-workspaces \
  CODEX_DOCK_KATA_OWNER_FIXTURE=/home/kata-test/owner-fixture/project \
  CODEX_DOCK_NODE_RESTART_REQUEST_DIR=/run/setup-node-restart \
  /acceptance.test -test.run "${CODEX_DOCK_KATA_ACCEPTANCE_RUN:-^TestKata(Acceptance|AuthenticationSession|EncryptedProfile|KeyringProfile|WorkspaceSharing|PolicyFeatures|PrimaryTerminal|GoAPI)$}" -test.v -test.count=1 -test.timeout=25m > /evidence/acceptance.txt 2>&1 &
 test_pid=$!
 while kill -0 "$test_pid" 2>/dev/null; do
  if [[ -f /run/setup-node-restart/request ]]; then
   read -r action cid < /run/setup-node-restart/request
   rm /run/setup-node-restart/request
   if [[ $action == namespace ]]; then
    [[ $cid =~ ^[0-9a-f]{64}$ ]]
    [[ -z $(ctr_node tasks list -q | awk -v cid="$cid" '$0==cid{print}') ]]
    python3 - "$cid" <<'PYBEFORE'
import json, os, sys
from pathlib import Path
cid = sys.argv[1]
stat = os.stat('/run/netns/cdx-' + cid[:24])
Path('/run/setup-node-restart/before.json').write_text(json.dumps({'device': stat.st_dev, 'inode': stat.st_ino}))
PYBEFORE
    ip netns del "cdx-${cid:0:24}"
    [[ ! -e /run/netns/cdx-${cid:0:24} ]]
   else
    [[ $action == restart ]]
   fi
   systemctl restart codex-dock-node
   ready=''
   for attempt in $(seq 1 60); do
    if cli vm ps -a --json > /dev/null 2>&1; then ready=1; break; fi
    sleep .2
   done
   [[ $ready == 1 ]]
   if [[ $action == namespace ]]; then
    python3 - "$cid" <<'PYAFTER'
import json, os, sys
from pathlib import Path
root = Path('/run/setup-node-restart')
cid = sys.argv[1]
stat = os.stat('/run/netns/cdx-' + cid[:24])
before = json.loads((root / 'before.json').read_text())
after = {'device': stat.st_dev, 'inode': stat.st_ino}
if before == after:
    raise SystemExit('stopped worker namespace was not replaced')
proof = {'operation': 'namespace', 'container_id': cid, 'removed': True, 'before': before, 'after': after}
(root / 'complete.tmp').write_text(json.dumps(proof))
os.replace(root / 'complete.tmp', root / 'complete')
PYAFTER
   else
    printf ready > /run/setup-node-restart/complete.tmp
    mv /run/setup-node-restart/complete.tmp /run/setup-node-restart/complete
   fi
  fi
  sleep .2
 done
 wait "$test_pid"
 if [[ -d /var/lib/codex-dock-node/engine/audit ]]; then
  /usr/local/bin/oaiso audit --directory /var/lib/codex-dock-node/engine/audit > /evidence/node-audit.json
  proof "node operation audit chain verified without pending results"
 fi
 proof "native Kata acceptance on newly installed node: ${CODEX_DOCK_KATA_ACCEPTANCE_RUN:-all}"
fi
if [[ ${CODEX_DOCK_KATA_ACCEPTANCE_RUN:-} == *ResourceControls* ]]; then
 # All suite-owned workers have been removed. Restore the normal node command
 # before exercising installation updates and rollback.
 rm /etc/systemd/system/codex-dock-node.service.d/resources.conf
 systemctl daemon-reload
 systemctl restart codex-dock-node
 for attempt in $(seq 1 150); do
  if cli runtime-check --json > /evidence/readiness-after-resources.json 2>/evidence/resource-node-reset.txt; then break; fi
  sleep .2
 done
 cli runtime-check --json > /evidence/readiness-after-resources.json
 proof 'resource fixture removed before standard update and recovery'
fi
# Case proxies intentionally retain their shared egress network for reuse.
# This disposable controller owns an initially empty node: remove only that
# expected, unused network, and retain evidence instead of masking other leaks.
curl --fail --silent --show-error --unix-socket /run/codex-dock-node.sock http://localhost/networks > /evidence/networks-after-acceptance.json
cleanup_egress=$(python3 - <<'PYCLEANUP'
import json
from pathlib import Path
networks = json.loads(Path('/evidence/networks-after-acceptance.json').read_text())
for network in networks:
    if network['Name'] != 'dock-net-proxy' or network.get('Containers'):
        raise SystemExit('unexpected or occupied network remains after acceptance; retain evidence')
if networks:
    if len(networks) != 1:
        raise SystemExit('ambiguous shared egress network')
    print('remove')
PYCLEANUP
)
if [[ $cleanup_egress == remove ]]; then cli network rm; fi
ctr_node images list -q | sort > /evidence/images-before-update.txt
/bundle-next/oaiso setup --python "$setup_python" --bundle /bundle-next --operator kata-test --offline --apply > /evidence/update.txt 2>&1
updated_release=$(release)
[[ $updated_release != "$initial_release" ]]
cli runtime-check --json > /evidence/readiness-updated.json
ctr_node images list -q | sort > /evidence/images-after-update.txt
cmp /evidence/images-before-update.txt /evidence/images-after-update.txt
proof 'successful matched-version update retains images and project files'
if [[ -f /var/lib/codex-dock-setup/release-floor.json ]]; then
 cp /var/lib/codex-dock-setup/release-floor.json /evidence/release-floor.json
 before_refusal=$(systemctl show --property MainPID --value codex-dock-node codex-dock-buildkit codex-dock-containerd)
 if /bundle/oaiso setup --python "$setup_python" --bundle /bundle --operator kata-test --offline --apply > /evidence/downgrade-refused.txt 2>&1; then
  echo 'implicit release downgrade unexpectedly succeeded' >&2; exit 1
 fi
 [[ $(< /evidence/downgrade-refused.txt) == *'refusing release downgrade'* ]]
 [[ $(systemctl show --property MainPID --value codex-dock-node codex-dock-buildkit codex-dock-containerd) == "$before_refusal" ]]
 [[ $(release) == "$updated_release" ]]
 proof 'implicit downgrade rejected before stopping services'
fi
# Fail only the newly staged BuildKit executable. Old release assets stay intact.
downgrade_args=()
if [[ $(/bundle/oaiso setup --help) == *'--allow-downgrade'* ]]; then downgrade_args+=(--allow-downgrade); fi
/bundle/oaiso setup --python "$setup_python" --bundle /bundle --operator kata-test --offline "${downgrade_args[@]}" --apply > /evidence/failed-update.txt 2>&1 &
update_pid=$!
faulted=''
for attempt in $(seq 1 3000); do
 for candidate in /opt/codex-dock/releases/*/bin/buildkitd; do
  if [[ -x $candidate && $candidate != "$updated_release/bin/buildkitd" && $candidate != "$initial_release/bin/buildkitd" ]]; then
   chmod 000 "$candidate"
   faulted=$candidate
   break
  fi
 done
 [[ -n $faulted ]] && break
 kill -0 "$update_pid" 2>/dev/null || break
 sleep .1
done
[[ -n $faulted ]]
if wait "$update_pid"; then echo 'faulted update unexpectedly succeeded' >&2; exit 1; fi
chmod 0755 "$faulted"
[[ $(release) == "$updated_release" ]]
cli runtime-check --json > /evidence/readiness-after-failure.json
[[ $(cat created) == 1000:1000 ]]
proof 'failed new service activation automatically restores previous working release'
/usr/local/bin/oaiso setup --python "$setup_python" --rollback --apply > /evidence/rollback.txt 2>&1
[[ $(release) == "$initial_release" ]]
cli runtime-check --json > /evidence/readiness-rollback.json
if [[ -f /evidence/release-floor.json ]]; then
 cmp /evidence/release-floor.json /var/lib/codex-dock-setup/release-floor.json
 if /bundle/oaiso setup --python "$setup_python" --bundle /bundle --operator kata-test --offline > /evidence/downgrade-after-rollback.txt 2>&1; then
  echo 'rollback silently lowered release floor' >&2; exit 1
 fi
 /bundle/oaiso setup --python "$setup_python" --bundle /bundle --operator kata-test --offline --allow-downgrade > /evidence/explicit-downgrade-plan.txt
 proof 'explicit rollback preserves high-water mark; older installation still requires explicit approval flag'
fi
cli vm ps -a --json > /evidence/final-vms.json
ctr_node containers list -q > /evidence/final-containers.txt
ctr_node tasks list -q > /evidence/final-tasks.txt
ip netns list > /evidence/final-netns.txt
nft list tables > /evidence/final-nft-tables.txt
python3 - <<'PYEMPTY'
import json
from pathlib import Path
if json.loads(Path('/evidence/final-vms.json').read_text())['vms']:
    raise SystemExit('VM inventory is not empty; inspect final-vms.json')
for name in ['final-containers.txt', 'final-tasks.txt', 'final-netns.txt']:
    if Path('/evidence', name).read_text().strip():
        raise SystemExit('resources remain; inspect ' + name)
for line in Path('/evidence/final-nft-tables.txt').read_text().splitlines():
    fields = line.split()
    if len(fields) > 2 and fields[2].startswith('cdx_'):
        raise SystemExit('owned firewall table remains; inspect final-nft-tables.txt')
PYEMPTY
proof 'explicit rollback restores matching CLI/node with no remaining containers, VMs or networks'
