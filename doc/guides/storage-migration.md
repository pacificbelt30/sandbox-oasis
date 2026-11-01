# 専用ストレージへ移行する

**日本語** | [English](../en/guides/storage-migration.md)

利用可能：**v0.0.7**。書き込み可能なrootfsには、OS領域と別の専用XFSでproject quotaのaccountingとenforcementが必要です。[容量制限](resource-limits.md)も確認してください。

## 保管する対象を分ける

| 対象 | 保管・復元 |
|---|---|
| VMのrootfs | 停止後の[disk checkpoint](disk-checkpoint.md)。標準entrypointが互換の新規VMへ復元 |
| workspace・追加共有 | ホスト側で別途バックアップ。数値UID/GID・権限も保持し、復元先を明示 |
| Codex専用DB | 対応版では[agent-state export/restore](agent-state.md#バックアップと別vmへの復元)。rootfs checkpointには含めない |
| HOME内の会話・設定・キャッシュ | rootfs checkpoint対象外。必要な利用者データは停止前に回収し、秘密を確認 |
| 認証・エージェントポリシー | ホストの既存プロファイル・設定を別途保全。VMのバックアップに認証ProxyのStoreや管理シークレットを混ぜない |
| RAM・ネットワーク・実行ID・トークン | 復元しない。新規VMとして検証・発行 |

バックアップは共有workspaceの外に、所有者だけが読めるディレクトリで保管します。任意の場所の秘密を自動判別する機能や、バックアップ暗号化はありません。ハッシュ検証は内容の一致を確認するもので、作成者の認証ではありません。

## 旧版から取り出す

前提：旧ホストの通常ユーザーとして実行。アプリの保存を完了し、`source-job`を自分のワーカー名に置き換えます。

```bash
install -d -m 0700 "$HOME/oaiso-backup"
oaiso stop source-job &&
  (umask 077; set -C; oaiso checkpoint create source-job > "$HOME/oaiso-backup/checkpoint.json")
```

v0.0.6には`checkpoint export`がありません。管理者が既存の配置台帳から信頼済み`ctr`を使って書き出します。下の例は既定の専用socketとnamespaceの場合です。`/home/alice`は保管先の所有者に置き換えます。

```bash
sudo python3 - <<'PY'
import json, os, subprocess
from pathlib import Path
backup = Path('/home/alice/oaiso-backup')
record = json.loads(Path('/var/lib/codex-dock-setup/installed.json').read_text())
info = json.loads((backup / 'checkpoint.json').read_text())
output = backup / 'checkpoint.oci'
fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
os.close(fd)
subprocess.run([str(Path(record['release']) / 'bin/ctr'),
               '--address', '/run/codex-dock-containerd/containerd.sock',
               '--namespace', 'codex-dock', 'images', 'export',
               '--skip-manifest-json', str(output), info['image']], check=True)
owner = backup.stat()
os.chown(output, owner.st_uid, owner.st_gid)
PY
```

- エラーになった書き出しは完成品と扱わず、元のcheckpointを保持して原因を確認してください。
- 新版同士では、`oaiso checkpoint export ID --output FILE`を使えます。
- workspaceなどの別バックアップを取り、元のVM・ストレージを復元確認まで保持します。

## 新しい保存先で復元する

1. 管理者が**別の保存先**に専用XFSを準備し、`/var/lib/codex-dock-containerd`へproject quota有効でmountします。setupは既存ext4を変換・formatしません。既存データがある領域へformatやmountを重ねないでください。
2. 対応するCLI・node・guard・loggerを配置し、通常ユーザーで`oaiso runtime-check`の`storage`が成功することを確認します。quota設定はカーネルから確認します。
3. バックアップを所有者専用で転送し、workspaceを数値UID/GIDと権限を保持して復元します。
4. 通常ユーザーでlogin・init後、取り込みます。保存元と同じ数値UID/GIDを使ってください。

```bash
oaiso checkpoint import "$HOME/oaiso-backup/checkpoint.oci"
```

出力されたIDを使い、自分の復元済みworkspaceを指定します。

```bash
read -r -p 'Imported checkpoint ID: ' checkpoint_id
```

```bash
oaiso run --checkpoint "$checkpoint_id" --name restored-job \
  --project /home/alice/restored-project --detach --keep --exec 'sleep infinity' &&
  oaiso connect restored-job
```

- Codex専用DBも復元する場合は、開始前に`--create-only`で作成して[DB復元手順](agent-state.md#バックアップと別vmへの復元)を実行します。
- 成果物・DBの整合性・起動後の書き込み・認証と通信規則を確認してから旧環境を片付けます。
- 取り込みはOCI形式・blob hash・標準entrypoint・除外対象を検査します。既存の同名imageを置き換えません。失敗時も保存元とバックアップを保持できます。
- 新版で作成したワーカーを旧版へ戻す手順ではありません。旧版へ戻す場合は、更新前の保管物と互換の専用環境で復元してください。

## 使用量を調べる

- `oaiso runtime-check --json`：quotaの有効性、専用ファイルシステムの空き容量、ワーカーごとの上限。
- TUIの詳細：書き込み可能なrootfsの使用byte／上限とinode数／上限。共有imageの総サイズ・workspace・DBディスクは含みません。
- 未対応node・quota照会失敗は「取得不可」と表示し、使用量0と解釈しません。
