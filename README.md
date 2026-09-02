api-gateway（Gin，HTTP :8080）→ gRPC（:9001/:9002）→ user-service / product-service（各自 GORM → MySQL）

gateway/repository/client.go	Repository 模式的泛化

product_repository.go	Update/Delete 用 RowsAffected==0 判存在性：省一次往返 + 无 TOCTOU 竞态

user_service.go	防用户枚举攻击

pkg/errors	错误码协议无关 + 边缘翻译；分支不透传 SQL 细节

pkg/jwt	Parse 强制 HMAC 白名单，防 alg-confusion 攻击

gateway/model/dto.go	反腐层；分↔元换算归 gateway
