# Oaiso local TypeScript SDK

- v0.0.4。Node.js 22以降。実行時の追加依存なし。
- 同じホストの`oaiso api stdio`を専用パイプで呼び、公開Go APIの作成・実行・照会・回収を使用します。
- 自動再送は行いません。管理socketの権限とGo APIの要求ID・失敗の意味を維持します。
- 導入・コマンド例・取消・復旧：[利用ガイド](https://pacificbelt30.github.io/sandbox-oasis/local-api-sdk/)。
- 型・上限：[JSON Schema](https://github.com/pacificbelt30/sandbox-oasis/blob/main/api/local-v1/schema.json)。
- `npm ci --ignore-scripts` → `npm run build`で生成した`dist/`を使用します。コンパイラは開発用依存です。

- v0.0.4の追加操作：ファイルの読書き・一覧（1回512KiB）、background job・offset付きログ、disk checkpointの保存・照会・削除・新規VMへの復元。
