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
    "fmt"
    "log"
    
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/s3"
    overwrite "github.com/ideamans/go-s3-overwrite"
)

func main() {
    sess := session.Must(session.NewSession())
    svc := s3.New(sess)
    
    err := overwrite.OverwriteS3Object(svc, "my-bucket", "path/to/file.txt",
        func(info overwrite.ObjectInfo, tmpFile *os.File) (bool, error) {
            // オブジェクトのメタデータはinfoで利用可能
            fmt.Printf("処理中: %s (サイズ: %d バイト)\n", 
                info.Key, *info.ContentLength)
            
            // ファイル内容を読み込んで変更
            content, err := io.ReadAll(tmpFile)
            if err != nil {
                return false, err
            }
            
            // 内容を処理（例：大文字に変換）
            modified := strings.ToUpper(string(content))
            
            // 一時ファイルに書き戻し
            tmpFile.Seek(0, 0)
            tmpFile.Truncate(0)
            tmpFile.WriteString(modified)
            
            // trueを返すと上書き、falseを返すとスキップ
            return true, nil
        })
    
    if err != nil {
        log.Fatal(err)
    }
}
```

### 例：シンプルACLの設定

```go
err := overwrite.OverwriteS3ObjectWithAcl(svc, "my-bucket", "public/image.jpg", "public-read",
    func(info overwrite.ObjectInfo, tmpFile *os.File) (bool, error) {
        // 10MBより大きいファイルはスキップ
        if *info.ContentLength > 10*1024*1024 {
            return false, nil
        }
        
        // ここで画像の最適化処理など...
        
        return true, nil
    })
```

### 例：JSONの整形

```go
err := overwrite.OverwriteS3Object(svc, "my-bucket", "data/config.json",
    func(info overwrite.ObjectInfo, tmpFile *os.File) (bool, error) {
        // JSONを読み込み
        data, err := io.ReadAll(tmpFile)
        if err != nil {
            return false, err
        }
        
        var jsonData interface{}
        if err := json.Unmarshal(data, &jsonData); err != nil {
            return false, fmt.Errorf("無効なJSON: %w", err)
        }
        
        // インデント付きで整形
        formatted, err := json.MarshalIndent(jsonData, "", "  ")
        if err != nil {
            return false, err
        }
        
        // メタデータを追加
        if info.Metadata == nil {
            info.Metadata = make(map[string]*string)
        }
        info.Metadata["formatted"] = aws.String("true")
        info.Metadata["formatted-at"] = aws.String(time.Now().Format(time.RFC3339))
        
        // 整形したJSONを書き込み
        tmpFile.Seek(0, 0)
        tmpFile.Truncate(0)
        _, err = tmpFile.Write(formatted)
        
        return true, err
    })
```

## APIリファレンス

### 関数

#### OverwriteS3Object

既存のACLを保持しながらS3オブジェクトを上書きします。

```go
func OverwriteS3Object(
    client S3Client,
    bucket string,
    key string,
    callback OverwriteCallback,
) error
```

**パラメータ:**

- `client`: S3Clientインターフェースを実装するAWS S3クライアント
- `bucket`: S3バケット名
- `key`: S3オブジェクトキー
- `callback`: オブジェクトを処理する関数

#### OverwriteS3ObjectWithAcl

特定のシンプルACLでS3オブジェクトを上書きします。

```go
func OverwriteS3ObjectWithAcl(
    client S3Client,
    bucket string,
    key string,
    acl string,
    callback OverwriteCallback,
) error
```

**パラメータ:**

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
type OverwriteCallback func(info ObjectInfo, tmpFile *os.File) (bool, error)
```

**パラメータ:**

- `info`: オブジェクトのメタデータ
- `tmpFile`: オブジェクトの内容を含む一時ファイル（読み書き可能）

**戻り値:**

- `bool`: trueでオブジェクトを上書き、falseでスキップ
- `error`: 処理中に発生したエラー

### S3Clientインターフェース

S3操作に必要な最小限のインターフェースです。AWS SDKの`*s3.S3`型はこのインターフェースを実装しています。

```go
type S3Client interface {
    GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error)
    GetObjectTagging(input *s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error)
    GetObjectAcl(input *s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error)
    PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error)
    PutObjectAcl(input *s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error)
}
```

## 動作の仕組み

1. オブジェクトを一時ファイルにダウンロード（0600権限）
2. オブジェクトメタデータからObjectInfo構造体を構築
3. メタデータと一時ファイルでコールバック関数を呼び出し
4. コールバックがtrueを返した場合：
   - 既存のタグとACLを取得
   - 保持された属性で変更されたコンテンツをアップロード
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