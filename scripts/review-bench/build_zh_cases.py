#!/usr/bin/env python3
"""Generate the self-built Chinese sample cases: small diffs written the way a
Chinese team writes code (Chinese comments, log strings, identifiers in
pinyin), each with planted defects annotated in Chinese. These are the
corpus half that scores Chinese naturalness, so the golden notes are the
tone we want the reviewer to match.

Usage: python3 scripts/review-bench/build_zh_cases.py <out_dir>
"""
import datetime, json, os, sys

CASES = [
 {"id": "zh/go-dingdan-nil", "lang": "Go", "title": "订单服务：增加按用户查询订单", "path": "internal/dingdan/service.go",
  "diff": """@@ -10,6 +10,22 @@ type Service struct {
 	repo Repo
 }
 
+// ChaXunYongHuDingDan 按用户 ID 查询订单列表，未找到时返回空切片。
+func (s *Service) ChaXunYongHuDingDan(ctx context.Context, yongHuID int64) ([]*DingDan, error) {
+	if yongHuID <= 0 {
+		return nil, fmt.Errorf("非法的用户 ID: %d", yongHuID)
+	}
+	list, err := s.repo.ListByUser(ctx, yongHuID)
+	if err != nil {
+		log.Printf("查询用户 %d 的订单失败: %v", yongHuID, err)
+	}
+	// 过滤掉已取消的订单
+	var jieGuo []*DingDan
+	for _, d := range list {
+		if d.ZhuangTai != ZhuangTaiYiQuXiao {
+			jieGuo = append(jieGuo, d)
+		}
+	}
+	return jieGuo, nil
+}
""",
  "golden": [
   {"from": 18, "to": 20, "cat": "Code Defect", "note": "`s.repo.ListByUser` 出错时只打了日志没有返回，下面会继续遍历 nil 的 `list` 并把错误当成“没有订单”返回给调用方，应该直接 `return nil, err`。"},
   {"from": 23, "to": 29, "cat": "Maintainability and Readability", "note": "注释说“未找到时返回空切片”，但 `jieGuo` 未初始化时返回的是 nil，JSON 序列化会变成 `null` 而不是 `[]`，建议 `jieGuo := make([]*DingDan, 0, len(list))`。"},
  ]},
 {"id": "zh/go-huancun-lock", "lang": "Go", "title": "本地缓存：支持过期时间", "path": "pkg/huancun/cache.go",
  "diff": """@@ -5,18 +5,30 @@ import (
 type Cache struct {
 	mu    sync.RWMutex
 	items map[string]item
+	// 默认过期时间，0 表示永不过期
+	moRenGuoQi time.Duration
 }
 
+type item struct {
+	value    any
+	guoQiShi time.Time // 过期时间点
+}
+
 func (c *Cache) Get(key string) (any, bool) {
 	c.mu.RLock()
-	defer c.mu.RUnlock()
 	it, ok := c.items[key]
-	return it.value, ok
+	c.mu.RUnlock()
+	if !ok {
+		return nil, false
+	}
+	// 已过期的条目顺手删掉，避免内存一直涨
+	if !it.guoQiShi.IsZero() && time.Now().After(it.guoQiShi) {
+		delete(c.items, key)
+		return nil, false
+	}
+	return it.value, true
 }
""",
  "golden": [
   {"from": 27, "to": 28, "cat": "Code Defect", "note": "`delete(c.items, key)` 在没有持有写锁的情况下修改 map，和并发的 `Get`/`Set` 一起跑会触发 `concurrent map writes` 直接 panic。删除前要 `c.mu.Lock()`，或者把过期清理交给单独的 goroutine。"},
  ]},
 {"id": "zh/py-zhifu-float", "lang": "Python", "title": "支付：计算优惠后金额", "path": "zhifu/jisuan.py",
  "diff": """@@ -1,4 +1,21 @@
 from decimal import Decimal
 
+
+def jisuan_shifu(jine: Decimal, youhui_lv: float, manjian: Decimal = Decimal("0")) -> Decimal:
+    \"\"\"计算实付金额。
+
+    jine: 订单原价（元）
+    youhui_lv: 折扣率，例如 0.85 表示八五折
+    manjian: 满减金额，直接从折后价里扣
+    \"\"\"
+    if youhui_lv <= 0 or youhui_lv > 1:
+        raise ValueError("折扣率必须在 (0, 1] 之间")
+    zhehou = jine * youhui_lv
+    shifu = zhehou - manjian
+    # 实付不能为负
+    if shifu < 0:
+        shifu = 0
+    return round(shifu, 2)
""",
  "golden": [
   {"from": 12, "to": 12, "cat": "Code Defect", "note": "`Decimal * float` 在 Python 里会直接抛 `TypeError`，这个函数根本跑不通；`youhui_lv` 应该用 `Decimal(str(youhui_lv))` 转换后再乘，金额计算也不该让 float 参与。"},
   {"from": 15, "to": 17, "cat": "Code Defect", "note": "`shifu = 0` 把 Decimal 换成了 int，返回类型和签名不一致，调用方做 `.quantize()` 之类的操作会报错，应写成 `Decimal(\"0\")`。"},
  ]},
 {"id": "zh/py-shangchuan-path", "lang": "Python", "title": "文件上传：保存用户头像", "path": "yonghu/touxiang.py",
  "diff": """@@ -3,6 +3,17 @@ import os
 
 TOUXIANG_DIR = "/data/touxiang"
 
+
+def baocun_touxiang(yonghu_id: int, wenjian_ming: str, neirong: bytes) -> str:
+    \"\"\"把用户上传的头像保存到磁盘，返回保存路径。\"\"\"
+    # 按用户 ID 分目录，避免单目录文件过多
+    mulu = os.path.join(TOUXIANG_DIR, str(yonghu_id))
+    os.makedirs(mulu, exist_ok=True)
+    lujing = os.path.join(mulu, wenjian_ming)
+    with open(lujing, "wb") as f:
+        f.write(neirong)
+    return lujing
""",
  "golden": [
   {"from": 11, "to": 13, "cat": "Security Vulnerability", "note": "`wenjian_ming` 直接来自用户上传，传 `../../etc/cron.d/x` 这种名字就能写到任意目录，属于路径穿越。应该只取 `os.path.basename` 并校验扩展名，或者干脆用服务端生成的随机文件名。"},
  ]},
 {"id": "zh/ts-denglu-token", "lang": "TypeScript", "title": "登录：记住登录状态", "path": "src/auth/denglu.ts",
  "diff": """@@ -12,6 +12,24 @@ export interface DengLuJieGuo {
   token: string;
 }
 
+/** 登录成功后把 token 存起来，刷新页面也能保持登录 */
+export function baoCunDengLu(jieGuo: DengLuJieGuo, jiZhuWo: boolean): void {
+  const cunChu = jiZhuWo ? localStorage : sessionStorage;
+  cunChu.setItem('token', jieGuo.token);
+  cunChu.setItem('yongHu', JSON.stringify(jieGuo.yongHu));
+  // 记录登录时间，用于前端判断是否需要重新登录
+  cunChu.setItem('dengLuShiJian', Date.now().toString());
+}
+
+export function huoQuToken(): string | null {
+  return localStorage.getItem('token') ?? sessionStorage.getItem('token');
+}
+
+export function yiDengLu(): boolean {
+  const shiJian = Number(localStorage.getItem('dengLuShiJian'));
+  // 7 天内有效
+  return Date.now() - shiJian < 7 * 24 * 60 * 60 * 1000;
+}
""",
  "golden": [
   {"from": 28, "to": 32, "cat": "Code Defect", "note": "`yiDengLu` 只读 `localStorage`，没勾“记住我”的用户登录时间存在 `sessionStorage`，`Number(null)` 是 0，`Date.now() - 0` 永远大于 7 天，这类用户会被当成未登录。要和 `huoQuToken` 一样两个 storage 都查。"},
  ]},
 {"id": "zh/ts-fenye-offbyone", "lang": "TypeScript", "title": "列表分页：计算总页数和当前页数据", "path": "src/utils/fenye.ts",
  "diff": """@@ -1,3 +1,19 @@
+export interface FenYeJieGuo<T> {
+  zongYeShu: number;
+  dangQianYe: T[];
+}
+
+/** 按每页 meiYe 条切分 shuJu，ye 从 1 开始 */
+export function fenYe<T>(shuJu: T[], ye: number, meiYe: number): FenYeJieGuo<T> {
+  if (meiYe <= 0) {
+    throw new Error('每页条数必须大于 0');
+  }
+  const zongYeShu = Math.floor(shuJu.length / meiYe);
+  const kaiShi = ye * meiYe;
+  return {
+    zongYeShu,
+    dangQianYe: shuJu.slice(kaiShi, kaiShi + meiYe),
+  };
+}
""",
  "golden": [
   {"from": 11, "to": 11, "cat": "Code Defect", "note": "总页数用 `Math.floor` 会把最后不满一页的数据吞掉，比如 10 条每页 3 条算出来是 3 页，第 10 条永远看不到，应该用 `Math.ceil`。"},
   {"from": 12, "to": 12, "cat": "Code Defect", "note": "注释说 `ye` 从 1 开始，但 `kaiShi = ye * meiYe` 是按 0 开始算的，第 1 页会直接跳过前 meiYe 条，应为 `(ye - 1) * meiYe`。"},
  ]},
 {"id": "zh/java-kucun-tx", "lang": "Java", "title": "库存扣减：下单时锁定库存", "path": "src/main/java/com/example/kucun/KuCunService.java",
  "diff": """@@ -20,6 +20,25 @@ public class KuCunService {
         this.kuCunMapper = kuCunMapper;
     }
 
+    /**
+     * 扣减库存并写一条流水。库存不足时抛出业务异常。
+     */
+    public void kouJian(Long shangPinId, int shuLiang) {
+        KuCun kuCun = kuCunMapper.selectById(shangPinId);
+        if (kuCun.getKeYong() < shuLiang) {
+            throw new YeWuException("库存不足，商品 " + shangPinId);
+        }
+        kuCun.setKeYong(kuCun.getKeYong() - shuLiang);
+        kuCunMapper.updateById(kuCun);
+
+        LiuShui liuShui = new LiuShui();
+        liuShui.setShangPinId(shangPinId);
+        liuShui.setBianDong(-shuLiang);
+        liuShuiMapper.insert(liuShui);
+        // TODO 后面补上事务
+        log.info("扣减库存成功: {} -{}", shangPinId, shuLiang);
+    }
""",
  "golden": [
   {"from": 27, "to": 32, "cat": "Code Defect", "note": "先查再改的“读-改-写”没有加锁也没加事务，两个请求同时扣减会把库存扣成负数（超卖）。应该用 `UPDATE ... SET ke_yong = ke_yong - ? WHERE id = ? AND ke_yong >= ?` 一条语句扣减并判断影响行数，再加 `@Transactional` 把流水一起写。"},
   {"from": 23, "to": 24, "cat": "Code Defect", "note": "`selectById` 查不到时返回 null，紧接着 `kuCun.getKeYong()` 会 NPE，给调用方的会是 500 而不是“商品不存在”的业务错误。"},
  ]},
 {"id": "zh/java-riqi-format", "lang": "Java", "title": "报表导出：日期列格式化", "path": "src/main/java/com/example/baobiao/RiQiGeShi.java",
  "diff": """@@ -1,8 +1,16 @@
 package com.example.baobiao;
 
 import java.text.SimpleDateFormat;
 import java.util.Date;
 
 public final class RiQiGeShi {
-    private RiQiGeShi() {}
+    // 报表里统一用这个格式，复用一个实例省得每次 new
+    private static final SimpleDateFormat GE_SHI = new SimpleDateFormat("yyyy-MM-dd HH:mm");
+
+    private RiQiGeShi() {}
+
+    /** 导出任务是多线程跑的，这里直接调用即可 */
+    public static String geShiHua(Date riQi) {
+        return GE_SHI.format(riQi);
+    }
 }
""",
  "golden": [
   {"from": 8, "to": 15, "cat": "Code Defect", "note": "`SimpleDateFormat` 不是线程安全的，注释又明说导出任务是多线程的，共享一个 static 实例会出现日期串位甚至 `ArrayIndexOutOfBoundsException`。换成 `DateTimeFormatter`（线程安全）或者每次调用新建。"},
  ]},
 {"id": "zh/go-peizhi-default", "lang": "Go", "title": "配置加载：支持环境变量覆盖超时", "path": "internal/peizhi/load.go",
  "diff": """@@ -15,10 +15,21 @@ type PeiZhi struct {
 	ChaoShi time.Duration
 }
 
 func Load() (*PeiZhi, error) {
 	pz := &PeiZhi{ChaoShi: 30 * time.Second}
+	// 允许用 CHAOSHI_MIAO 覆盖默认超时（单位：秒）
+	if v := os.Getenv("CHAOSHI_MIAO"); v != "" {
+		miao, err := strconv.Atoi(v)
+		if err != nil {
+			return nil, fmt.Errorf("CHAOSHI_MIAO 不是整数: %q", v)
+		}
+		pz.ChaoShi = time.Duration(miao)
+	}
+	if pz.ChaoShi <= 0 {
+		pz.ChaoShi = 30 * time.Second
+	}
 	return pz, nil
 }
""",
  "golden": [
   {"from": 26, "to": 26, "cat": "Code Defect", "note": "`time.Duration(miao)` 的单位是纳秒，不是注释里说的秒，配 `CHAOSHI_MIAO=30` 实际超时只有 30ns，所有请求都会立刻超时。应该写 `time.Duration(miao) * time.Second`。"},
  ]},
 {"id": "zh/py-rizhi-secret", "lang": "Python", "title": "第三方对接：调用短信网关", "path": "tongzhi/duanxin.py",
  "diff": """@@ -5,6 +5,20 @@ import requests
 
 WANGGUAN = "https://sms.example.com/send"
 
+
+def fasong_duanxin(shouji: str, neirong: str, miyao: str) -> bool:
+    \"\"\"调用短信网关发送通知，返回是否成功。\"\"\"
+    canshu = {"mobile": shouji, "content": neirong, "sign": miyao}
+    logger.info("发送短信请求: %s", canshu)
+    try:
+        resp = requests.post(WANGGUAN, json=canshu)
+    except requests.RequestException as e:
+        logger.error("短信网关异常: %s", e)
+        return False
+    # 网关约定 code=0 表示成功
+    return resp.json().get("code") == 0
""",
  "golden": [
   {"from": 11, "to": 12, "cat": "Security Vulnerability", "note": "把包含 `miyao`（签名密钥）和用户手机号的 `canshu` 整个打进 info 日志，密钥和个人信息会进日志平台。日志里应去掉 `sign` 并对手机号脱敏。"},
   {"from": 14, "to": 14, "cat": "Code Defect", "note": "`requests.post` 没有设置 `timeout`，网关卡住时这个请求会无限等待，把调用方的 worker 挂死，至少要 `timeout=(3, 10)` 这类配置。"},
  ]},
]

def main():
    out_dir = sys.argv[1]
    today = datetime.date.today().isoformat()
    for c in CASES:
        diff = c["diff"]
        # Every planted line number refers to the new side of the single hunk.
        case = {
            "id": c["id"], "source": "self-built-zh", "kind": "chinese", "added_at": today,
            "pr": {"url": "https://example.invalid/" + c["id"], "owner": "zh", "repo": c["id"].split("/")[1], "number": 1,
                   "base_sha": "0" * 40, "head_sha": "1" * 40, "title": c["title"], "description": "",
                   "language": c["lang"], "category": "self-built", "changed_lines": sum(l.startswith(("+", "-")) for l in diff.splitlines())},
            "diffs": [{"old_path": c["path"], "new_path": c["path"], "diff": diff.strip("\n"), "new_file": False, "deleted_file": False, "renamed_file": False}],
            "golden": [{"path": c["path"], "side": "right", "from_line": g["from"], "to_line": g["to"], "category": g["cat"], "context": "Diff Level", "note": g["note"], "source": "human"} for g in c["golden"]],
            "negatives": [],
        }
        d = os.path.join(out_dir, c["id"].split("/")[1]); os.makedirs(d, exist_ok=True)
        with open(os.path.join(d, "case.json"), "w") as fh:
            json.dump(case, fh, ensure_ascii=False, indent=1)
        print("ok", c["id"], c["lang"], "golden", len(case["golden"]))

if __name__ == "__main__":
    main()
