# go-s3-overwrite パッケージ設計書

## 1. 概要

`go-s3-overwrite`は、AWS S3のオブジェクトを上書きする際に、メタデータ、タグ、ACLなどの属性を自動的に保持するシンプルなGoパッケージです。

### 1.1 解決する課題

S3のPutObject操作は内部的にオブジェクトを削除して再作成するため、以下の属性が失われます：

- オブジェクトのタグ
- ACL設定
- カスタムメタデータ
- ContentType、CacheControlなどの属性

本パッケージは、コールバック関数を使用してこれらの属性を透過的に保持します。

### 1.2 設計原則

1. **シンプル**: 2つの関数のみを提供
2. **直感的**: コールバックベースの使いやすいAPI
3. **安全**: 適切なエラーハンドリングとクリーンアップ
4. **テスタブル**: モックを使った単体テストが容易

## 2. パッケージ構成

```
github.com/[organization]/go-s3-overwrite/
├── README.md          # 英語版README
├── README_ja.md       # 日本語版README
├── LICENSE            # MITライセンス
├── go.mod
├── go.sum
├── .env.example
├── .gitignore
├── .github/
│   └── workflows/
│       ├── test.yml   # 単体テスト
│       └── e2e.yml    # 結合テスト
├── overwrite.go       # メイン実装
├── overwrite_test.go  # 単体テスト
├── e2e_test.go        # 結合テスト
└── examples/
    └── main.go        # 使用例

```

## 3. API仕様

### 3.1 基本構造体

```go
// ObjectInfo contains S3 object metadata
type ObjectInfo struct {
    Bucket          string
    Key             string
    ContentType     *string
    ContentLength   *int64
    ETag            *string
    LastModified    *time.Time
    Metadata        map[string]*string
    StorageClass    *string
    TagCount        *int64
    VersionId       *string
}

// OverwriteCallback defines the callback function signature
// The callback receives object info and the path to the source file.
// It returns:
// - overwritingFilePath: the path to the file to upload (empty string to skip overwrite)
// - autoRemove: if true, the file will be automatically removed after upload (only if different from srcFilePath)
// - err: any error that occurred
type OverwriteCallback func(info ObjectInfo, srcFilePath string) (overwritingFilePath string, autoRemove bool, err error)
```

### 3.2 OverwriteS3Object

既存のACLを保持しながらS3オブジェクトを条件付きで上書きします。

```go
func OverwriteS3Object(
    client *s3.S3,
    bucket string,
    key string,
    callback OverwriteCallback,
) error
```

**パラメータ:**

- `client`: AWS S3クライアント
- `bucket`: S3バケット名
- `key`: S3オブジェクトキー
- `callback`: 処理を行うコールバック関数

**コールバック関数:**

- 引数1: オブジェクトのメタデータ情報
- 引数2: ダウンロードされた一時ファイルへのパス
- 戻り値1: アップロードするファイルのパス（空文字列""で上書きをスキップ）
- 戻り値2: 自動削除フラグ（trueの場合、アップロード後にファイルを自動削除。ただしsrcFilePathと異なる場合のみ）
- 戻り値3: エラー（エラーが返された場合、関数全体もそのエラーを返す）

### 3.3 OverwriteS3ObjectWithAcl

シンプルACLを指定してS3オブジェクトを条件付きで上書きします。

```go
func OverwriteS3ObjectWithAcl(
    client *s3.S3,
    bucket string,
    key string,
    acl string,
    callback OverwriteCallback,
) error
```

**パラメータ:**

- `client`: AWS S3クライアント
- `bucket`: S3バケット名
- `key`: S3オブジェクトキー
- `acl`: シンプルACL（"private", "public-read", "public-read-write", "authenticated-read"）
- `callback`: 処理を行うコールバック関数

## 4. 実装詳細

### 4.1 処理フロー

1. GetObjectでオブジェクトをダウンロード
2. 一時ファイルに保存（権限0600）
3. ObjectInfo構造体を構築
4. コールバック関数を呼び出し（一時ファイルのパスを渡す）
5. 空でないファイルパスが返された場合：
   - GetObjectTaggingでタグを取得
   - GetObjectAcl でACLを取得（OverwriteS3Objectの場合）
   - 返されたファイルパスの内容でPutObject実行
   - 必要に応じてPutObjectAclでWRITE権限を復元
6. 一時ファイルをクリーンアップ

### 4.2 エラーハンドリング

- すべてのエラーは`fmt.Errorf`の`%w`でラップ
- 一時ファイルは必ずクリーンアップ
- 部分的な失敗（PutObject成功、PutObjectAcl失敗）も適切に処理

## 5. 使用例

```go
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "log"
    "os"
    "time"
    
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/s3"
    overwrite "github.com/[organization]/go-s3-overwrite"
)

func main() {
    sess := session.Must(session.NewSession())
    svc := s3.New(sess)
    
    // JSONファイルを整形して上書き
    err := overwrite.OverwriteS3Object(svc, "my-bucket", "data/config.json",
        func(info overwrite.ObjectInfo, srcFilePath string) (string, bool, error) {
            fmt.Printf("Processing: %s/%s (size: %d bytes)\n", 
                info.Bucket, info.Key, *info.ContentLength)
            
            // 10MB以上のファイルはスキップ
            if *info.ContentLength > 10*1024*1024 {
                return "", false, nil
            }
            
            // JSONを読み込み
            data, err := os.ReadFile(srcFilePath)
            if err != nil {
                return "", false, err
            }
            
            var jsonData interface{}
            if err := json.Unmarshal(data, &jsonData); err != nil {
                return "", false, fmt.Errorf("invalid JSON: %w", err)
            }
            
            // 整形
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
            
            // 新しいファイルに書き込み
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
    
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Println("Successfully formatted JSON file")
}
```

## 6. テスト戦略

### 6.1 単体テスト

モックS3クライアントを使用した依存関係のないテスト：

```go
type mockS3Client struct {
    GetObjectFunc        func(*s3.GetObjectInput) (*s3.GetObjectOutput, error)
    GetObjectTaggingFunc func(*s3.GetObjectTaggingInput) (*s3.GetObjectTaggingOutput, error)
    GetObjectAclFunc     func(*s3.GetObjectAclInput) (*s3.GetObjectAclOutput, error)
    PutObjectFunc        func(*s3.PutObjectInput) (*s3.PutObjectOutput, error)
    PutObjectAclFunc     func(*s3.PutObjectAclInput) (*s3.PutObjectAclOutput, error)
}
```

テストケース：

- 正常系：上書き実行とスキップ
- エラー系：各API呼び出しの失敗
- コールバックのエラー処理
- 一時ファイルのクリーンアップ確認

### 6.2 結合テスト

環境変数`TEST_BUCKET`またはGitHub Actions シークレット`TEST_BUCKET`が設定されている場合のみ実行：

```go
func TestE2E(t *testing.T) {
    bucket := os.Getenv("TEST_BUCKET")
    if bucket == "" {
        t.Skip("TEST_BUCKET not set, skipping E2E tests")
    }
    // 実際のS3に対するテスト
}
```

## 7. CI/CD設定

### 7.1 単体テスト (.github/workflows/test.yml)

```yaml
name: Test

on:
  push:
    branches: [ '**' ]
  pull_request:
    branches: [ '**' ]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'
    
    - name: Test
      run: go test -v -race -coverprofile=coverage.out ./...
    
    - name: Upload coverage
      uses: codecov/codecov-action@v3
```

### 7.2 結合テスト (.github/workflows/e2e.yml)

```yaml
name: E2E Test

on:
  push:
    branches: [ '**' ]
  pull_request:
    branches: [ '**' ]

jobs:
  e2e:
    runs-on: ubuntu-latest
    if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository
    
    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'
    
    - name: E2E Test
      if: env.TEST_BUCKET != ''
      env:
        TEST_BUCKET: ${{ secrets.TEST_BUCKET }}
        AWS_ACCESS_KEY_ID: ${{ secrets.AWS_ACCESS_KEY_ID }}
        AWS_SECRET_ACCESS_KEY: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
        AWS_REGION: ${{ secrets.AWS_REGION || 'us-east-1' }}
      run: go test -v -tags=e2e ./...
```

## 8. 環境設定

### 8.1 .env.example

```bash
# Required for E2E tests
TEST_BUCKET=your-test-bucket-name
AWS_ACCESS_KEY_ID=your-access-key
AWS_SECRET_ACCESS_KEY=your-secret-key
AWS_REGION=us-east-1
```

### 8.2 必要なIAM権限

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
      "Resource": "arn:aws:s3:::your-test-bucket/*"
    }
  ]
}
```

## 9. ライセンス

MIT License

Copyright (c) 2024 [Your Organization]

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
