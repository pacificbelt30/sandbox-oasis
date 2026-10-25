# キーリングで認証情報の鍵を管理する

**日本語** | [English](../en/guides/keyring.md)

- **v0.0.3以降で利用できます。**
- 新規の`oaiso login`はキーリング保存が既定です。既存プロファイルの方式は変更しません。
- APIキー・OAuthトークンはageで暗号化した`.vault`へ保存。キーリングにはage秘密鍵と改ざん検証用の鍵を保存します。
- 作業VMにはキーリング・D-Busソケット・実資格情報を渡しません。

## 環境に合う起動方法

| 環境 | 操作 |
|---|---|
| デスクトップのキーリングが利用可能 | 通常の`oaiso login`を実行。解錠済みなら追加入力不要 |
| SSH・WSL・GUIなしのVM | `oaiso auth keyring session`で専用セッションを開く |
| キーリングを使用しない | `login --encrypt`で製品用パスフレーズ方式を選ぶ |
| 明示的に平文保存する | `login --plaintext`。0700ディレクトリ・0600ファイル |

- GUI・TPM・USBキーは不要です。
- キーリングが利用不能なら保存を拒否し、別方式へ自動変更しません。提供元へのログインを始める前に保存・取得・削除を確認します。

## 1. 依存を準備する

- v0.0.3以降の`oaiso setup`は、不足するD-Bus・GNOME Keyring・`secret-tool`を導入します。
- オフライン配置では事前に同じパッケージが必要です。
- 手動導入するUbuntu環境の例：

```bash
sudo apt-get update
sudo apt-get install --no-install-recommends dbus-daemon gnome-keyring libsecret-tools
```

| ツール | 役割・公式資料 |
|---|---|
| D-Bus | 利用者のプロセス間通信。[公式手順](https://dbus.freedesktop.org/doc/dbus-run-session.1.html) |
| GNOME Keyring | 鍵の保管・解錠。[公式プロジェクト](https://gitlab.gnome.org/GNOME/gnome-keyring)・[daemonの操作](https://manpages.debian.org/unstable/gnome-keyring/gnome-keyring-daemon.1.en.html) |
| `secret-tool` | Secret Serviceへの保存・取得・削除。[公式実装](https://github.com/GNOME/libsecret/blob/main/tool/secret-tool.c) |

- GNOMEのデスクトップ全体は不要です。配布パッケージの依存としてGUI関連ライブラリが入る場合はあります。

## 2. デスクトップで利用する

```bash
oaiso auth keyring check
oaiso login --agent codex --method subscription --profile work
oaiso init --agent codex
```

```text
Keyring: ready (store/read/delete verified; no provider request).
```

- `check`はランダムな試験項目だけを保存・再取得・削除します。既存の秘密は表示しません。
- `DBUS_SESSION_BUS_ADDRESS`が未設定でも、0700・本人所有の利用者runtimeディレクトリにある`bus`ソケットを探索します。
- OS側でPAM連携されていれば、ログインパスワードによってキーリングも解錠されます。OaisoがOSパスワードを取得する処理はありません。
- PAM設定は自動変更しません。SSH公開鍵認証など、OSパスワードを入力しないログインでは別途キーリングの解錠が必要です。[PAMの公式説明](https://wiki.gnome.org/Projects/GnomeKeyring/Pam/Manual)。

## 3. GUIなしで利用する

```bash
oaiso auth keyring session
```

1. 初回は専用パスフレーズを非表示で2回入力。次回は1回。
2. D-BusとGNOME Keyringを起動し、専用のキーリングを解錠。
3. 開いたシェルで通常のコマンドを実行。

```bash
oaiso auth keyring check
oaiso login --agent claude --method api-key --profile claude-work
oaiso init --agent claude
oaiso claude
exit
```

- セッション用のパスフレーズは12〜4096バイト。OSのログインパスワードとは独立です。
- GNOME Keyringの保存先は`~/.config/codex-dock/keyring/keyrings/`。デスクトップのキーリングとは分離します。
- セッション内の複数CLIが、同じ解錠済みサービスを利用します。
- シェル終了時に専用キーリングとD-Busを終了。暗号化した保管ファイルは保持します。
- 同じ保管先の専用セッションを同時に2つ開くことは拒否します。
- シェルを終了しても、起動済み認証Proxyはメモリの鍵を保持します。Proxyもロックする場合は、終了前に`oaiso auth profile lock NAME`を実行してください。
- 次回、専用キーリングに保管したプロファイルを解錠する場合も、`keyring session`内で操作します。

コマンド1つだけを実行する場合：

```bash
oaiso auth keyring session -- oaiso auth keyring check
```

- 非対話入力はグローバルの`--passphrase-fd`で専用FDを指定できます。パスフレーズを引数・環境変数・平文ファイルへ保存しないでください。

## 既存プロファイル

- 既存の0600平文・パスフレーズ形式はそのまま利用できます。
- 保存方式を切り替える場合は、キーリングが利用可能な環境で別名の新しいプロファイルへログインしてください。
- キーリング間の自動コピーや、既存パスフレーズ形式の自動変換は行いません。
- [ageの形式・ロック操作](encrypted-credentials.md)、[検証結果と未確認事項](../testing.md)。
