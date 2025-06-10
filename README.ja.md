# go-s3-overwrite

[![English](https://img.shields.io/badge/lang-English-blue.svg)](README.md)

S3オブジェクトのメタデータ、タグ、ACLを保持しながら上書きするシンプルなGoパッケージです。

## 概要

標準のPutObject操作でS3オブジェクトを上書きすると、AWS S3は内部的にオブジェクトを削除して再作成するため、以下の情報が失われます：

- オブジェクトタグ
- ACL設定
- カスタムメタデータ
- ContentTypeやCacheControlなどの属性

このパッケージは、これらの属性を自動的に保持する2つのシンプルな関数を提供することで、この問題を解決します。

## インストール

```bash
go get github.com/ideamans/go-s3-overwrite
```

## 使用方法

### 基本的な例：既存のACLを保持

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "strings"
    
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    overwrite "github.com/ideamans/go-s3-overwrite"
)

func main() {
    // AWS設定をロード
    cfg, err := config.LoadDefaultConfig(context.TODO())
    if err != nil {
        log.Fatal(err)
    }
    svc := s3.NewFromConfig(cfg)
    
    err = overwrite.OverwriteS3Object(context.Background(), svc, "my-bucket", "path/to/file.txt",
        func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
            // オブジェクトのメタデータはinfoで利用可能
            fmt.Printf("処理中: %s (サイズ: %d バイト)\n", 
                info.Key, *info.ContentLength)
            
            // ファイル内容を読み込む
            content, err := os.ReadFile(srcFilePath)
            if err != nil {
                return "", false, err
            }
            
            // 内容を処理（例：大文字に変換）
            modified := strings.ToUpper(string(content))
            
            // 変更された内容で新しいファイルを作成
            modifiedFile, err := os.CreateTemp("", "modified-*.txt")
            if err != nil {
                return "", false, err
            }
            defer modifiedFile.Close()
            
            if _, err := modifiedFile.WriteString(modified); err != nil {
                os.Remove(modifiedFile.Name())
                return "", false, err
            }
            
            // 変更されたファイルのパスを返して上書き、または""を返してスキップ
            // autoRemove = true で一時ファイルを自動的にクリーンアップ
            return modifiedFile.Name(), true, nil
        })
    
    if err != nil {
        log.Fatal(err)
    }
}
```

### 例：シンプルACLの設定

```go
err := overwrite.OverwriteS3ObjectWithAcl(context.Background(), svc, "my-bucket", "public/image.jpg", "public-read",
    func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
        // 10MBより大きいファイルはスキップ
        if *info.ContentLength > 10*1024*1024 {
            return "", false, nil
        }
        
        // ここで画像の最適化処理など...
        // 例えば、変更が不要な場合は同じファイルを返す
        
        return srcFilePath, false, nil
    })
```

### 例：JSONの整形

```go
import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "time"
    
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    overwrite "github.com/ideamans/go-s3-overwrite"
)

// AWS設定をロード
 cfg, err := config.LoadDefaultConfig(context.TODO())
if err != nil {
    log.Fatal(err)
}
svc := s3.NewFromConfig(cfg)

err = overwrite.OverwriteS3Object(context.Background(), svc, "my-bucket", "data/config.json",
    func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
        // JSONを読み込み
        data, err := os.ReadFile(srcFilePath)
        if err != nil {
            return "", false, err
        }
        
        var jsonData interface{}
        if err := json.Unmarshal(data, &jsonData); err != nil {
            return "", false, fmt.Errorf("無効なJSON: %w", err)
        }
        
        // インデント付きで整形
        formatted, err := json.MarshalIndent(jsonData, "", "  ")
        if err != nil {
            return "", false, err
        }
        
        // メタデータを追加
        if info.Metadata == nil {
            info.Metadata = make(map[string]*string)
        }
        info.Metadata["formatted"] = aws.String("true")
        info.Metadata["formatted-at"] = aws.String(time.Now().Format(time.RFC3339))
        
        // 整形されたJSONで新しいファイルを作成
        formattedFile, err := os.CreateTemp("", "formatted-*.json")
        if err != nil {
            return "", false, err
        }
        defer formattedFile.Close()
        
        if _, err := formattedFile.Write(formatted); err != nil {
            os.Remove(formattedFile.Name())
            return "", false, err
        }
        
        return formattedFile.Name(), true, nil
    })
```

## APIリファレンス

### 関数

#### OverwriteS3Object

既存のACLを保持しながらS3オブジェクトを上書きします。

```go
func OverwriteS3Object(
    ctx context.Context,
    client S3Client,
    bucket string,
    key string,
    callback OverwriteCallback,
) error
```

**パラメータ:**

- `ctx`: 操作のコンテキスト
- `client`: S3Clientインターフェースを実装するAWS S3クライアント
- `bucket`: S3バケット名
- `key`: S3オブジェクトキー
- `callback`: オブジェクトを処理する関数

#### OverwriteS3ObjectWithAcl

特定のシンプルACLでS3オブジェクトを上書きします。

```go
func OverwriteS3ObjectWithAcl(
    ctx context.Context,
    client S3Client,
    bucket string,
    key string,
    acl string,
    callback OverwriteCallback,
) error
```

**パラメータ:**

- `ctx`: 操作のコンテキスト
- `client`: S3Clientインターフェースを実装するAWS S3クライアント
- `bucket`: S3バケット名
- `key`: S3オブジェクトキー
- `acl`: 適用するシンプルACL（`"private"`、`"public-read"`、`"public-read-write"`、`"authenticated-read"`）
- `callback`: オブジェクトを処理する関数

### 型

#### ObjectInfo

コールバック関数に渡されるS3オブジェクトのメタデータを含みます。

```go
type ObjectInfo struct {
    Bucket        string
    Key           string
    ContentType   *string
    ContentLength *int64
    ETag          *string
    LastModified  *time.Time
    Metadata      map[string]*string
    StorageClass  *string
    TagCount      *int64
    VersionId     *string
}
```

#### OverwriteCallback

オブジェクトを処理するコールバック関数のシグネチャです。

```go
type OverwriteCallback func(info ObjectInfo, srcFilePath string) (overwritingFilePath string, autoRemove bool, err error)
```

**パラメータ:**

- `info`: オブジェクトのメタデータ
- `srcFilePath`: オブジェクトの内容を含む一時ファイルへのパス

**戻り値:**

- `overwritingFilePath`: アップロードするファイルのパス（空文字列""を返すと上書きをスキップ）
- `autoRemove`: trueの場合、`overwritingFilePath`のファイルはアップロード後に自動的に削除されます（`srcFilePath`と異なる場合のみ）
- `err`: 処理中に発生したエラー

### S3Clientインターフェース

S3操作に必要な最小限のインターフェースです。AWS SDK v2の`*s3.Client`型はこのインターフェースを実装しています。

```go
type S3Client interface {
    GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
    GetObjectTagging(ctx context.Context, params *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error)
    GetObjectAcl(ctx context.Context, params *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error)
    PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
    PutObjectAcl(ctx context.Context, params *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error)
}
```

## 動作の仕組み

1. オブジェクトを一時ファイルにダウンロード
2. オブジェクトメタデータからObjectInfo構造体を構築
3. メタデータと一時ファイルのパスでコールバック関数を呼び出し
4. コールバックが空でないファイルパスを返した場合：
   - 既存のタグとACLを取得
   - 返されたパスからファイル内容を保持された属性でアップロード
   - 必要に応じてWRITE権限を復元（PutObjectAcl経由）
5. 一時ファイルを必ずクリーンアップ

## 必要なIAM権限

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:GetObjectTagging",
        "s3:GetObjectAcl",
        "s3:PutObject",
        "s3:PutObjectAcl"
      ],
      "Resource": "arn:aws:s3:::your-bucket/*"
    }
  ]
}
```

## テスト

### 単体テスト

```bash
# 単体テストを実行
go test -v ./...

# カバレッジ付きでテストを実行
go test -v -race -cover ./...
```

### E2Eテスト

このパッケージには、実際のS3バケットに対して機能を検証する包括的なE2Eテストが含まれています。

#### 前提条件

1. テスト用のS3バケットを作成
2. バケットのパブリックアクセスとACLを有効化（ACL保持テストに必要）
3. AWS認証情報の設定（AWSプロファイルまたはアクセスキー経由）

#### E2Eテストの実行

```bash
# AWSプロファイルを使用（推奨）
TEST_BUCKET=your-test-bucket AWS_PROFILE=your-profile go test -v -tags=e2e ./...

# AWSアクセスキーを使用
TEST_BUCKET=your-test-bucket \
  AWS_ACCESS_KEY_ID=your-key \
  AWS_SECRET_ACCESS_KEY=your-secret \
  AWS_REGION=ap-northeast-1 \
  go test -v -tags=e2e ./...

# 便利な.envファイルを作成
echo "TEST_BUCKET=your-test-bucket" > .env
echo "AWS_PROFILE=your-profile" >> .env
go test -v -tags=e2e ./...
```

#### E2Eテストカバレッジ

E2Eテストは以下を検証します：

- ACL保持（シンプルおよび複雑なACL）
- 特殊文字を含むメタデータの保持
- URLエンコーディングを含むタグの保持
- Content-TypeとCache-Control属性
- 複数のグランティータイプ（ID、URI、メール）
- エラー処理とエッジケース
- 一時ファイルのクリーンアップ

## コントリビューション

プルリクエストは歓迎します。大きな変更については、まず変更したい内容についてissueを開いて議論してください。

適切にテストを更新してください。

## ライセンス

[MIT](LICENSE)