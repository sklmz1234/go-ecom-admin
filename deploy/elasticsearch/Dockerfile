# 商品搜索引擎镜像（阶段 5A）：官方 ES + 预装 IK 中文分词器。
#
# 为什么必须 IK：ES 自带的 standard 分词器把中文按单字切——"降噪耳机"
# 变成 降/噪/耳/机 四个独立 term，搜"耳机"能命中但相关度排序基本是
# 随机噪音（任何含"耳"或"机"的商品得分一样）。IK 按词切：
# ik_max_word（索引时最细粒度："降噪耳机"→降噪/耳机/降噪耳机）+
# ik_smart（查询时粗粒度），中文搜索的召回和排序才像真正的电商。
#
# 版本纪律：IK 插件版本号必须与 ES 版本逐位对应（插件 manifest 里写死
# elasticsearch.version，不匹配直接拒绝加载），所以两处都要 pin 死，
# 升级 ES 时一起改。get.infini.cloud 是 IK 官方 README 给的安装源。
FROM docker.elastic.co/elasticsearch/elasticsearch:8.15.3
RUN elasticsearch-plugin install --batch https://get.infini.cloud/elasticsearch/analysis-ik/8.15.3
