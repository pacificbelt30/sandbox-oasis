#!/bin/bash
# Benign v0.0.6 migration rehearsal, only inside an owned disposable host.
set -euo pipefail
[[ -f /.dockerenv && ${CODEX_DOCK_DISPOSABLE_NODE:-} == 1 ]]
operator() { runuser -u kata-test -- env HOME=/home/kata-test OPENAI_API_KEY=sk-legacy-fixture-never-valid "$@"; }
cli() { operator /usr/local/bin/oaiso "$@"; }
release() { python3 -c 'import json; print(json.load(open("/var/lib/codex-dock-setup/installed.json"))["release"])'; }
ctr_node() { "$(release)/bin/ctr" --address /run/codex-dock-containerd/containerd.sock --namespace codex-dock "$@"; }
mkdir -p /evidence /var/cache/codex-dock-setup
for archive in /cache-input/*; do cp "$archive" /var/cache/codex-dock-setup/; done
chmod -R go-w /var/cache/codex-dock-setup
[[ $(stat -f -c %T /var/lib/codex-dock-containerd) == ext2/ext3 ]]
/bundle/oaiso setup --python /usr/bin/python3 --bundle /bundle --operator kata-test --offline --apply > /evidence/legacy-setup.txt 2>&1
ctr_node images import /images.tar > /evidence/legacy-images.txt
install -d -o kata-test -g 1000 -m 0700 /home/kata-test/.config/codex-dock /home/kata-test/project
python3 - <<'PY'
import os,json
from pathlib import Path
p=Path('/home/kata-test/.config/codex-dock/config.toml')
p.write_text('default_image = '+json.dumps(os.environ['CODEX_DOCK_KATA_TEST_IMAGE'])+'\nproxy_image = '+json.dumps(os.environ['CODEX_DOCK_KATA_PROXY_IMAGE'])+'\nauto_build = false\n')
p.chmod(0o600);os.chown(p,1000,1000)
PY
cli init > /evidence/legacy-init.txt 2>&1
cli run --name legacy-source --project /home/kata-test/project --detach --keep --no-internet --exec 'exec sleep 600' > /evidence/legacy-worker.txt 2>&1
cli exec legacy-source -- python3 -c "from pathlib import Path;Path('/opt/disk-state/keep').write_text('legacy-rootfs-preserved');Path('/workspace/migration-workspace').write_text('legacy-workspace-preserved')"
cli stop legacy-source > /evidence/legacy-stop.txt
cli checkpoint create legacy-source > /evidence/legacy-checkpoint.json
ref=$(python3 -c 'import json;print(json.load(open("/evidence/legacy-checkpoint.json"))["image"])')
# v0.0.6 has no checkpoint export command. This is the documented admin path;
# native image service labels are restored only after new-node validation.
ctr_node images export --skip-manifest-json /evidence/legacy-checkpoint.oci "$ref"
chmod 0600 /evidence/legacy-checkpoint.oci
# The source remains usable before any destructive cleanup. A rejected
# destination import cannot require deleting or mutating this source.
cli start legacy-source > /evidence/legacy-source-restart.txt
cli exec legacy-source -- python3 -c "from pathlib import Path;assert Path('/opt/disk-state/keep').read_text()=='legacy-rootfs-preserved';assert Path('/workspace/migration-workspace').read_text()=='legacy-workspace-preserved'"
cli stop legacy-source >> /evidence/legacy-stop.txt
install -m 0600 /home/kata-test/project/migration-workspace /evidence/migration-workspace
sha256sum /evidence/legacy-checkpoint.oci /evidence/migration-workspace > /evidence/legacy-SHA256SUMS
cli rm --force legacy-source
cli checkpoint rm "${ref##*:}"
cli proxy stop
cli proxy rm
cli network rm
[[ -z $(ctr_node tasks list -q) && -z $(ctr_node containers list -q) ]]
echo 'VERIFIED: v0.0.6 ext4 stopped checkpoint export, workspace copy, source restart and owned resource cleanup'
