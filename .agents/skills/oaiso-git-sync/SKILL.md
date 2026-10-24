---
name: oaiso-git-sync
description: "OaisoのGit同期、競合解消、変更のcommit・pushと対象CI確認を依頼されたときに使う。検証のみの依頼からpush・公開を始めない。"
---

# Gitを同期し、競合を解消する

## 状態を確認する

リポジトリのルートで確認する。

```bash
git status --short
git branch --show-current
git log -5 --oneline --decorate
git remote
```

- 作業ブランチ、同期先、利用者の未コミット変更、自分の変更を区別する。ログへ資格情報を含むremote URLを出さない。
- ユーザーの依頼と既存の許可に合わせてfetch・merge・commit・pushを行う。同期の依頼だけでリリースタグを作らない。
- 未コミット変更を`reset --hard`・`clean`で捨てない。既存の作業と衝突する場合は、必要に応じて分離したworktreeを使う。

## 同期・競合を確認する

1. 対象remoteをfetchし、現在のHEADから増える変更を読む。共有履歴を勝手にrebase・force-pushしない。
2. merge時は変更の目的と呼出し側を確認する。競合マーカーが消えただけで解消済みとしない。
3. 認証・CAS／接続世代・失効・通信制限・資源予約・停止／削除・版照合の条件が、両方の変更で保たれるか調べる。
4. 振る舞いが変わる解決には、必要な回帰確認を[テストスキル](../oaiso-test/SKILL.md)で行う。実行経路に影響すれば実Kataも確認する。

## commit・pushする

- 対象ファイルを確認して個別にstageする。利用者の別作業、資格情報、生ログ、配布バイナリ、大きな一時baselineを含めない。
- `git diff --cached --check`とstage済み差分を確認し、問題と変更後の動作が伝わるcommitメッセージを書く。
- pushが拒否されたらremoteの新しい変更を確認する。拒否をforce-pushで回避しない。
- CIはpushした正確なSHAの実行を確認する。別コミットの成功、ジョブの開始、PR専用の結果だけで成功と報告しない。
- 最終報告にはコミット、同期先、検証結果、残る競合・未確認事項を示す。文書更新だけなら公開版番号を変えない。
