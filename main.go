package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Table 单张表格：表头 + 内容行
type Table struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// Memo 一条备忘录
type Memo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	UpdatedAt int64  `json:"updatedAt"` // unix 秒
}

// Store 所有数据
type Store struct {
	Tables  []*Table `json:"tables"`
	Memos   []*Memo  `json:"memos"`
	PinHash string   `json:"pinHash,omitempty"` // 锁屏密码的 SHA-256，空表示未设密码
}

var (
	mu    sync.Mutex
	store = Store{
		Tables: []*Table{
			{ID: newID(), Name: "表格 1", Headers: []string{"姓名", "日期", "内容"},
				Rows: [][]string{{"张三", "2026-11-04", "示例内容"}}},
		},
	}
	dataFile = "data.json"
)

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hashPin 密码哈希（不存明文）
func hashPin(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])
}

var pinRe = regexp.MustCompile(`^\d{4,6}$`)

// loadData 启动时从 data.json 加载；兼容旧版单表格格式
func loadData() {
	b, err := os.ReadFile(dataFile)
	if err != nil {
		return
	}
	var s Store
	if err := json.Unmarshal(b, &s); err == nil && len(s.Tables) > 0 {
		if s.Memos == nil {
			s.Memos = []*Memo{}
		}
		mu.Lock()
		store = s
		mu.Unlock()
		return
	}
	// 旧格式：{headers, rows}
	var d struct {
		Headers []string   `json:"headers"`
		Rows    [][]string `json:"rows"`
	}
	if err := json.Unmarshal(b, &d); err == nil && len(d.Headers) > 0 {
		mu.Lock()
		store = Store{Tables: []*Table{{
			ID: newID(), Name: "表格 1", Headers: d.Headers, Rows: d.Rows,
		}}}
		mu.Unlock()
	}
}

func saveStore() {
	b, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(dataFile, b, 0644); err != nil {
		log.Println("写文件失败:", err)
	}
}

// findTable 按 id 查找（调用前须持锁）
func findTable(id string) *Table {
	for _, t := range store.Tables {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// findMemo 按 id 查找备忘录（调用前须持锁）
func findMemo(id string) *Memo {
	for _, m := range store.Memos {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// handleIndex 返回编辑页面
func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

// handleTables GET 返回表格列表
func handleTables(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	type item struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RowCount int    `json:"rowCount"`
	}
	list := make([]item, 0, len(store.Tables))
	for _, t := range store.Tables {
		list = append(list, item{ID: t.ID, Name: t.Name, RowCount: len(t.Rows)})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(list)
}

// handleTableOp 表格管理操作：create / rename / delete
func handleTableOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Op   string `json:"op"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	switch req.Op {
	case "create":
		name := req.Name
		if name == "" {
			name = fmt.Sprintf("表格 %d", len(store.Tables)+1)
		}
		t := &Table{ID: newID(), Name: name, Headers: []string{"列 1", "列 2", "列 3"}}
		store.Tables = append(store.Tables, t)
	case "rename":
		t := findTable(req.ID)
		if t == nil {
			http.Error(w, "table not found", http.StatusNotFound)
			return
		}
		if req.Name != "" {
			t.Name = req.Name
		}
	case "delete":
		for i, t := range store.Tables {
			if t.ID == req.ID {
				store.Tables = append(store.Tables[:i], store.Tables[i+1:]...)
				break
			}
		}
		if len(store.Tables) == 0 {
			store.Tables = append(store.Tables, &Table{
				ID: newID(), Name: "表格 1", Headers: []string{"列 1", "列 2", "列 3"},
			})
		}
	default:
		http.Error(w, "unknown op", http.StatusBadRequest)
		return
	}
	saveStore()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, `{"ok":true}`)
}

// handleData GET /data?id=xxx 返回某张表的数据
func handleData(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	t := findTable(r.URL.Query().Get("id"))
	if t == nil {
		http.Error(w, "table not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(t)
}

// handleSave POST /save?id=xxx 保存某张表
func handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var t Table
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	cur := findTable(r.URL.Query().Get("id"))
	if cur == nil {
		http.Error(w, "table not found", http.StatusNotFound)
		return
	}
	cur.Headers = t.Headers
	cur.Rows = t.Rows
	saveStore()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, `{"ok":true}`)
}

// handleExport GET /export?id=xxx 导出某张表为 CSV
func handleExport(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	t := findTable(r.URL.Query().Get("id"))
	mu.Unlock()
	if t == nil {
		http.Error(w, "table not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, t.Name))
	// 写 BOM，保证 Excel 打开中文不乱码
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	// 使用 CRLF：Excel/WPS 才能正确识别引号内单元格的换行
	cw.UseCRLF = true
	cw.Write(t.Headers)
	cw.WriteAll(t.Rows)
	cw.Flush()
}

// handleMemos GET 返回备忘录列表（按更新时间倒序）
func handleMemos(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	list := make([]*Memo, len(store.Memos))
	copy(list, store.Memos)
	mu.Unlock()

	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })

	type item struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Preview   string `json:"preview"`
		UpdatedAt int64  `json:"updatedAt"`
	}
	out := make([]item, 0, len(list))
	for _, m := range list {
		p := strings.ReplaceAll(strings.TrimSpace(m.Content), "\n", " ")
		if len([]rune(p)) > 40 {
			p = string([]rune(p)[:40]) + "…"
		}
		out = append(out, item{ID: m.ID, Title: m.Title, Preview: p, UpdatedAt: m.UpdatedAt})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}

// handleMemo GET /memo?id= 取详情；POST /memo?id= 保存
func handleMemo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")

	if r.Method == http.MethodGet {
		mu.Lock()
		m := findMemo(id)
		var cp Memo
		if m != nil {
			cp = *m
		}
		mu.Unlock()
		if m == nil {
			http.Error(w, "memo not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(cp)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	m := findMemo(id)
	if m == nil {
		http.Error(w, "memo not found", http.StatusNotFound)
		return
	}
	m.Title = in.Title
	m.Content = in.Content
	m.UpdatedAt = time.Now().Unix()
	saveStore()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(m)
}

// handleMemoOp 备忘录管理：create / delete
func handleMemoOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Op string `json:"op"`
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	var newMemo *Memo
	switch req.Op {
	case "create":
		newMemo = &Memo{ID: newID(), Title: "无标题备忘", Content: "", UpdatedAt: time.Now().Unix()}
		store.Memos = append(store.Memos, newMemo)
	case "delete":
		for i, m := range store.Memos {
			if m.ID == req.ID {
				store.Memos = append(store.Memos[:i], store.Memos[i+1:]...)
				break
			}
		}
	default:
		http.Error(w, "unknown op", http.StatusBadRequest)
		return
	}
	saveStore()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if newMemo != nil {
		json.NewEncoder(w).Encode(newMemo)
		return
	}
	fmt.Fprint(w, `{"ok":true}`)
}

// handlePin 锁屏密码：GET 查询是否已设置；POST op=set|clear|reset
func handlePin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method == http.MethodGet {
		mu.Lock()
		set := store.PinHash != ""
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]bool{"set": set})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Op     string `json:"op"`
		Pin    string `json:"pin"`
		OldPin string `json:"oldPin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	// 设/清密码前校验旧密码
	if req.Op == "set" || req.Op == "clear" {
		if store.PinHash != "" && hashPin(req.OldPin) != store.PinHash {
			http.Error(w, "old pin wrong", http.StatusForbidden)
			return
		}
	}
	switch req.Op {
	case "set":
		if !pinRe.MatchString(req.Pin) {
			http.Error(w, "pin must be 4-6 digits", http.StatusBadRequest)
			return
		}
		store.PinHash = hashPin(req.Pin)
	case "clear", "reset":
		store.PinHash = ""
	default:
		http.Error(w, "unknown op", http.StatusBadRequest)
		return
	}
	saveStore()
	fmt.Fprint(w, `{"ok":true}`)
}

// handlePinVerify POST 校验密码
func handlePinVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Pin string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	mu.Lock()
	ok := store.PinHash == "" || hashPin(req.Pin) == store.PinHash
	mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]bool{"ok": ok})
}

// resetPin 清除锁屏密码：优先通过本地接口（服务运行时），否则直接改数据文件
func resetPin(port int) {
	url := fmt.Sprintf("http://127.0.0.1:%d/pin", port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(`{"op":"reset"}`))
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			fmt.Println("✓ 锁屏密码已清除，回到页面点「解锁」即可进入")
			return
		}
		fmt.Printf("✗ 重置失败: %s\n", resp.Status)
		os.Exit(1)
	}

	// 服务没在运行：直接改 data.json
	b, rerr := os.ReadFile(dataFile)
	if rerr != nil {
		fmt.Printf("✗ 服务未启动，且找不到 %s\n", dataFile)
		os.Exit(1)
	}
	var s Store
	if err := json.Unmarshal(b, &s); err != nil {
		fmt.Println("✗ 数据文件解析失败:", err)
		os.Exit(1)
	}
	s.PinHash = ""
	out, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(dataFile, out, 0644); err != nil {
		fmt.Println("✗ 写入失败:", err)
		os.Exit(1)
	}
	fmt.Println("✓ 锁屏密码已清除（直接修改了 data.json），重新运行 ./run.sh 即可")
}

func main() {
	// 命令行重置锁屏密码：./table-editor --reset-pin [端口]
	if len(os.Args) > 1 && os.Args[1] == "--reset-pin" {
		port := 8080
		if len(os.Args) > 2 {
			if p, err := strconv.Atoi(os.Args[2]); err == nil {
				port = p
			}
		}
		resetPin(port)
		return
	}

	port := 8080
	if len(os.Args) > 1 {
		if p, err := strconv.Atoi(os.Args[1]); err == nil {
			port = p
		}
	}
	loadData()

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/tables", handleTables)
	http.HandleFunc("/tables/op", handleTableOp)
	http.HandleFunc("/data", handleData)
	http.HandleFunc("/save", handleSave)
	http.HandleFunc("/export", handleExport)
	http.HandleFunc("/memos", handleMemos)
	http.HandleFunc("/memo", handleMemo)
	http.HandleFunc("/memos/op", handleMemoOp)
	http.HandleFunc("/pin", handlePin)
	http.HandleFunc("/pin/verify", handlePinVerify)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("表格编辑器已启动: http://localhost%s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}

const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>表格编辑器</title>
<style>
  * { box-sizing: border-box; }
  body { font-family: -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif;
         margin: 0; padding: 20px 24px; background: #f5f6f8; color: #222; }
  h1 { font-size: 20px; margin: 0 0 14px; }

  /* 表格标签页 */
  .tabs { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; align-items: center; }
  .tab { display: inline-flex; align-items: center; gap: 6px; padding: 7px 12px;
         border: 1px solid #d9d9d9; border-radius: 6px; background: #fff;
         cursor: pointer; font-size: 13px; user-select: none; }
  .tab:hover { border-color: #1677ff; }
  .tab.active { background: #1677ff; border-color: #1677ff; color: #fff; }
  .tab .cnt { font-size: 11px; opacity: .7; }
  .tab .close { border: none; background: transparent; cursor: pointer; font-size: 12px;
                color: #999; padding: 0; line-height: 1; }
  .tab .close:hover { color: #ff4d4f; }
  .tab.active .close { color: rgba(255,255,255,.8); }
  .tab.active .close:hover { color: #fff; }
  .tab-add { display: inline-flex; align-items: center; padding: 7px 12px;
             border: 1px dashed #bbb; border-radius: 6px; background: transparent;
             cursor: pointer; font-size: 13px; color: #555; }
  .tab-add:hover { border-color: #1677ff; color: #1677ff; }

  .toolbar { display: flex; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; align-items: center; }
  button { padding: 6px 14px; border: 1px solid #ccc; border-radius: 6px;
           background: #fff; cursor: pointer; font-size: 13px; }
  button:hover { background: #eee; }
  button.primary { background: #1677ff; border-color: #1677ff; color: #fff; }
  button.primary:hover { background: #4096ff; }
  #status { font-size: 13px; color: #52c41a; opacity: 0; transition: opacity .3s; }
  #status.show { opacity: 1; }

  .table-wrap { background: #fff; border-radius: 8px; box-shadow: 0 1px 4px rgba(0,0,0,.08);
                overflow: auto; }
  table { border-collapse: collapse; min-width: 600px; width: 100%; }
  th, td { border: 1px solid #e5e5e5; padding: 0; position: relative; }
  th { background: #fafafa; cursor: grab; user-select: none; }
  th:active { cursor: grabbing; }
  th.drag-over { box-shadow: inset 3px 0 0 #1677ff; }
  th.dragging { opacity: .4; }
  .del-col { position: absolute; right: 2px; top: 50%; transform: translateY(-50%);
             border: none; background: transparent; color: #ff4d4f; cursor: pointer;
             font-size: 12px; opacity: 0; padding: 0 2px; line-height: 1; }
  th:hover .del-col { opacity: 1; }
  th input { width: 100%; border: none; background: transparent; padding: 10px 12px;
             font-weight: 600; font-size: 14px; outline: none; }
  td[contenteditable] { padding: 10px 12px; font-size: 14px; min-width: 120px;
                        min-height: 41px; outline: none; cursor: text;
                        white-space: pre-wrap; word-break: break-word; line-height: 1.5; }
  td[contenteditable]:focus { background: #e6f4ff; box-shadow: inset 0 0 0 2px #1677ff; }
  td.row-op { width: 46px; text-align: center; padding: 10px 2px; }
  .row-del { border: none; background: transparent; color: #ff4d4f; cursor: pointer; opacity: 0; }
  tr:hover .row-del { opacity: 1; }
  .empty-tip { padding: 40px; text-align: center; color: #999; font-size: 14px; }
  .row-add-bar { display: block; width: 100%; border: none; border-top: 1px dashed #d9d9d9;
                 background: #fafafa; color: #1677ff; padding: 10px; font-size: 13px;
                 cursor: pointer; }
  .row-add-bar:hover { background: #f0f7ff; }

  /* 顶部导航 */
  .nav { display: flex; gap: 4px; margin-bottom: 16px; border-bottom: 1px solid #e5e5e5; }
  .nav button { border: none; border-bottom: 2px solid transparent; border-radius: 0;
                background: transparent; padding: 8px 16px; font-size: 14px; color: #666; }
  .nav button:hover { background: #f0f0f0; }
  .nav button.active { color: #1677ff; border-bottom-color: #1677ff; font-weight: 600; background: transparent; }

  /* 快捷键提示条 */
  .hint-bar { display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
              font-size: 12px; color: #8a8a8a; margin: -6px 0 14px; }
  .hint-bar .item { display: inline-flex; align-items: center; gap: 5px; }
  kbd { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px;
        background: #fff; border: 1px solid #d9d9d9; border-bottom-width: 2px; border-radius: 4px;
        padding: 1px 5px; color: #444; line-height: 15px; min-width: 18px; text-align: center;
        display: inline-block; }
  .hint-more { margin-left: auto; border: none; background: transparent; color: #1677ff;
               cursor: pointer; font-size: 12px; padding: 0; }
  .hint-more:hover { background: transparent; text-decoration: underline; }

  /* 快捷键面板 */
  #kbd-modal { position: fixed; inset: 0; z-index: 9000; display: none; padding: 20px;
               align-items: center; justify-content: center; background: rgba(0,0,0,.38); }
  #kbd-modal.show { display: flex; }
  .kbd-card { background: #fff; border-radius: 12px; width: min(580px, 100%);
              max-height: 84vh; overflow: auto; padding: 18px 22px 14px;
              box-shadow: 0 12px 40px rgba(0,0,0,.24); }
  .kbd-card h2 { font-size: 16px; margin: 0; display: flex; align-items: center; gap: 6px; }
  .kbd-card .close { margin-left: auto; border: none; background: transparent; font-size: 16px;
                     color: #999; cursor: pointer; line-height: 1; padding: 2px 4px; }
  .kbd-card .close:hover { background: transparent; color: #333; }
  .kbd-card .sub { font-size: 12px; color: #999; margin: 6px 0 4px; }
  .kbd-group { font-size: 12px; font-weight: 600; color: #1677ff; margin: 16px 0 4px; }
  .kbd-row { display: flex; align-items: center; gap: 12px; padding: 7px 0;
             border-bottom: 1px dashed #f0f0f0; }
  .kbd-row:last-child { border-bottom: none; }
  .kbd-keys { display: flex; gap: 4px; align-items: center; flex-shrink: 0; min-width: 132px; }
  .kbd-keys .plus { font-size: 10px; color: #c0c0c0; }
  .kbd-desc { font-size: 13px; color: #444; line-height: 1.45; }

  /* 备忘录 */
  #view-memos { display: none; gap: 12px; height: calc(100vh - 190px); min-height: 420px; }
  .memo-side { width: 250px; flex-shrink: 0; background: #fff; border-radius: 8px;
               box-shadow: 0 1px 4px rgba(0,0,0,.08); display: flex; flex-direction: column; overflow: hidden; }
  .memo-new { margin: 10px; padding: 8px; border: 1px dashed #bbb; border-radius: 6px;
              background: #fff; color: #555; cursor: pointer; font-size: 13px; }
  .memo-new:hover { border-color: #1677ff; color: #1677ff; }
  .memo-list { flex: 1; overflow: auto; }
  .memo-item { padding: 10px 12px; border-bottom: 1px solid #f2f2f2; cursor: pointer; }
  .memo-item:hover { background: #fafafa; }
  .memo-item.active { background: #e6f4ff; }
  .memo-item .t { font-size: 13px; font-weight: 600; color: #222; white-space: nowrap;
                  overflow: hidden; text-overflow: ellipsis; }
  .memo-item .p { font-size: 12px; color: #999; margin-top: 3px; white-space: nowrap;
                  overflow: hidden; text-overflow: ellipsis; }
  .memo-item .d { font-size: 11px; color: #bbb; margin-top: 3px; }
  .memo-main { flex: 1; background: #fff; border-radius: 8px; padding: 16px 18px;
               box-shadow: 0 1px 4px rgba(0,0,0,.08); display: flex; flex-direction: column; min-width: 0; }
  #memo-title { border: none; outline: none; font-size: 18px; font-weight: 600; padding: 4px 0;
                font-family: inherit; color: #222; }
  .memo-meta { font-size: 12px; color: #999; margin: 2px 0 10px; display: flex;
               justify-content: space-between; align-items: center; }
  #memo-body { flex: 1; border: none; outline: none; resize: none; font-size: 14px;
               line-height: 1.75; font-family: inherit; color: #333; }
  .memo-foot { display: flex; justify-content: space-between; align-items: center;
               border-top: 1px solid #f2f2f2; padding-top: 10px; }
  #memo-status { font-size: 12px; color: #52c41a; opacity: 0; transition: opacity .3s; }
  #memo-status.show { opacity: 1; }
  .memo-empty { margin: auto; text-align: center; color: #bbb; font-size: 14px; }

  /* 锁屏 */
  .lock-btn { margin-left: auto; border: 1px solid #ffccc7 !important; color: #cf1322 !important;
              border-radius: 6px; padding: 5px 12px !important; }
  .lock-btn:hover { background: #fff1f0 !important; }
  #lock-screen { position: fixed; inset: 0; z-index: 9999; display: none;
                 flex-direction: column; align-items: center; justify-content: center; gap: 16px;
                 background: linear-gradient(160deg, #1b2430 0%, #0f1720 100%);
                 color: #e8eef7; user-select: none; }
  #lock-screen.show { display: flex; }
  #lock-clock { font-size: 68px; font-weight: 200; letter-spacing: 3px;
                font-variant-numeric: tabular-nums; }
  #lock-date { font-size: 14px; color: #8fa3b8; margin-top: -8px; }
  .lock-hint { font-size: 13px; color: #7b8fa3; margin-top: 8px; }
  #lock-pin-wrap { display: none; flex-direction: column; align-items: center; gap: 8px; }
  #lock-pin { width: 200px; text-align: center; letter-spacing: 8px; font-size: 20px;
              padding: 10px; border-radius: 8px; border: 1px solid rgba(255,255,255,.25);
              background: rgba(255,255,255,.08); color: #fff; outline: none; }
  #lock-pin:focus { border-color: #4096ff; }
  #lock-err { color: #ff7875; font-size: 13px; height: 16px; }
  #lock-unlock { padding: 10px 30px; font-size: 15px; border-radius: 8px;
                 border: 1px solid rgba(255,255,255,.25); background: rgba(255,255,255,.08);
                 color: #fff; cursor: pointer; }
  #lock-unlock:hover { background: rgba(255,255,255,.18); }
  .lock-set { font-size: 12px; color: #6b7f93; cursor: pointer;
              text-decoration: underline; margin-top: 4px; }
  .lock-set:hover { color: #9fb4c8; }
</style>
</head>
<body>
<h1>📋 表格与备忘录</h1>

<div class="nav">
  <button class="active" data-view="tables" onclick="switchView('tables')">📋 表格</button>
  <button data-view="memos" onclick="switchView('memos')">📝 备忘录</button>
  <button class="lock-btn" onclick="lockNow()" title="锁屏（⌘/Ctrl + Shift + L）">🔒 锁屏</button>
</div>

<!-- ===== 快捷键提示条 ===== -->
<div class="hint-bar">
  <span class="item"><kbd class="mod-key">Ctrl</kbd><kbd>S</kbd>保存当前内容</span>
  <span class="item"><kbd class="mod-key">Ctrl</kbd><kbd>Shift</kbd><kbd>L</kbd>锁屏</span>
  <span class="item"><kbd class="alt-key">Alt</kbd><kbd>↑</kbd><kbd>↓</kbd><kbd>←</kbd><kbd>→</kbd>移动单元格</span>
  <span class="item"><kbd>?</kbd>查看全部快捷键</span>
  <button class="hint-more" onclick="toggleKbd()">全部快捷键 ›</button>
</div>

<!-- ===== 表格视图 ===== -->
<div id="view-tables">
<div class="tabs" id="tabs"></div>

<div class="toolbar">
  <button class="primary" onclick="save()">💾 保存当前表格</button>
  <button onclick="addColumn()">➕ 加一列</button>
  <button onclick="addRow()">➕ 加一行</button>
  <button onclick="exportCSV()">⬇️ 导出 CSV</button>
  <span id="status"></span>
</div>
<div class="table-wrap" id="wrap"></div>
</div>

<!-- ===== 备忘录视图 ===== -->
<div id="view-memos">
  <aside class="memo-side">
    <button class="memo-new" onclick="newMemo()">＋ 新建备忘</button>
    <div class="memo-list" id="memo-list"></div>
  </aside>
  <section class="memo-main" id="memo-main"></section>
</div>

<!-- ===== 快捷键面板 ===== -->
<div id="kbd-modal" onclick="if (event.target === this) toggleKbd(false)">
  <div class="kbd-card">
    <h2>⌨️ 快捷键 <button class="close" onclick="toggleKbd(false)" title="关闭（Esc）">✕</button></h2>
    <div class="sub">锁屏时所有快捷键暂停生效；输入框内按下表中组合键同样有效</div>

    <div class="kbd-group">表格 · 单元格导航</div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="alt-key">Alt</kbd><span class="plus">+</span><kbd>↓</kbd></span>
      <span class="kbd-desc">下一行；已在最后一行则<b>自动新增一行</b>并落进去</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="alt-key">Alt</kbd><span class="plus">+</span><kbd>↑</kbd></span>
      <span class="kbd-desc">上一行（首行停住）</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="alt-key">Alt</kbd><span class="plus">+</span><kbd>←</kbd></span>
      <span class="kbd-desc">左一格；在行首时跳到<b>上一行最后一格</b></span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="alt-key">Alt</kbd><span class="plus">+</span><kbd>→</kbd></span>
      <span class="kbd-desc">右一格；在行尾时跳到<b>下一行第一格</b>，最后一格再加一行</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>↑</kbd><kbd>↓</kbd><kbd>←</kbd><kbd>→</kbd></span>
      <span class="kbd-desc">普通方向键只在单元格内移动文字光标，不跳格</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>Enter</kbd></span>
      <span class="kbd-desc">单元格内换行（内容支持多行）</span>
    </div>

    <div class="kbd-group">表格 · 鼠标操作</div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>拖动表头</kbd></span>
      <span class="kbd-desc">左右调整列顺序，数据跟着一起移动</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>双击标签</kbd></span>
      <span class="kbd-desc">重命名当前表格</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>悬停 ✕</kbd></span>
      <span class="kbd-desc">表头右上的 ✕ 删除整列，行尾的 ✕ 删除整行（均有二次确认）</span>
    </div>

    <div class="kbd-group">备忘录</div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="mod-key">Ctrl</kbd><span class="plus">+</span><kbd>S</kbd></span>
      <span class="kbd-desc">立即保存（停止输入 0.6 秒也会自动保存）</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>切换备忘</kbd></span>
      <span class="kbd-desc">自动先保存当前内容，不丢字</span>
    </div>

    <div class="kbd-group">全局</div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="mod-key">Ctrl</kbd><span class="plus">+</span><kbd>S</kbd></span>
      <span class="kbd-desc">保存（表格 / 备忘录按当前页面自动判断）</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd class="mod-key">Ctrl</kbd><span class="plus">+</span><kbd>Shift</kbd><span class="plus">+</span><kbd>L</kbd></span>
      <span class="kbd-desc">立即锁屏（防误触，刷新页面仍保持锁定）</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>?</kbd></span>
      <span class="kbd-desc">打开本快捷键面板</span>
    </div>
    <div class="kbd-row">
      <span class="kbd-keys"><kbd>Esc</kbd></span>
      <span class="kbd-desc">关闭本面板</span>
    </div>
  </div>
</div>

<!-- ===== 锁屏遮罩 ===== -->
<div id="lock-screen">
  <div id="lock-clock">--:--:--</div>
  <div id="lock-date"></div>
  <div class="lock-hint">🔒 已锁屏 · 防止误触</div>
  <div id="lock-pin-wrap">
    <input id="lock-pin" type="password" inputmode="numeric" maxlength="6"
           placeholder="输入密码" onkeydown="if(event.key==='Enter'){event.preventDefault();tryUnlock();}">
    <div id="lock-err"></div>
  </div>
  <button id="lock-unlock" onclick="tryUnlock()">点击解锁</button>
  <div class="lock-set" id="lock-set" onclick="setPin()">设置密码（可选）</div>
  <div class="lock-set" style="margin-top:-8px" onclick="forgotPin()">忘记密码？</div>
</div>

<script>
let tables = [];          // [{id,name,rowCount}]
let currentId = null;     // 当前表格 id
let data = { headers: [], rows: [] };

async function loadTables() {
  tables = await (await fetch('/tables')).json();
  if (!currentId || !tables.find(t => t.id === currentId)) {
    currentId = tables.length ? tables[0].id : null;
  }
  renderTabs();
  if (currentId) await loadTable(currentId);
  else { data = { headers: [], rows: [] }; render(); }
}

async function loadTable(id) {
  data = await (await fetch('/data?id=' + id)).json();
  render();
}

function renderTabs() {
  const el = document.getElementById('tabs');
  el.innerHTML = '';
  tables.forEach(t => {
    const tab = document.createElement('div');
    tab.className = 'tab' + (t.id === currentId ? ' active' : '');
    tab.innerHTML = '<span>' + esc(t.name) + '</span><span class="cnt">(' + t.rowCount + ')</span>';
    tab.title = '点击切换，双击重命名';
    tab.onclick = () => { if (currentId !== t.id) { currentId = t.id; renderTabs(); loadTable(t.id); } };
    tab.ondblclick = async () => {
      const name = prompt('重命名表格：', t.name);
      if (name && name !== t.name) {
        await op({ op: 'rename', id: t.id, name: name });
        t.name = name;
        renderTabs();
      }
    };
    if (tables.length > 1 || true) {  // 单表格也允许删除（删除后自动建一张空的）
      const btn = document.createElement('button');
      btn.className = 'close'; btn.textContent = '✕';
      btn.title = '删除此表格';
      btn.onclick = async e => {
        e.stopPropagation();
        if (!confirm('删除表格「' + t.name + '」？数据不可恢复。')) return;
        await op({ op: 'delete', id: t.id });
        currentId = null;
        loadTables();
      };
      tab.appendChild(btn);
    }
    el.appendChild(tab);
  });
  const add = document.createElement('button');
  add.className = 'tab-add';
  add.textContent = '＋ 新建表格';
  add.onclick = async () => {
    const name = prompt('新表格名称：', '表格 ' + (tables.length + 1));
    if (name === null) return;
    await op({ op: 'create', name: name.trim() });
    currentId = null;   // 重新加载后跳到新表（列表末尾）
    const list = await (await fetch('/tables')).json();
    if (list.length) { currentId = list[list.length - 1].id; }
    loadTables();
  };
  el.appendChild(add);
}

async function op(body) {
  const res = await fetch('/tables/op', { method: 'POST',
    headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body) });
  if (!res.ok) alert('操作失败：' + await res.text());
}

/* ---------- 表格编辑 ---------- */
function render() {
  const wrap = document.getElementById('wrap');
  if (!data.headers || !data.headers.length) {
    wrap.innerHTML = '<div class="empty-tip">暂无表头，点击「加一列」开始编辑</div>';
    return;
  }
  let html = '<table><thead><tr>';
  html += '<th style="width:46px">#</th>';
  data.headers.forEach((h, i) => {
    html += '<th draggable="true" ondragstart="dragStartCol(event,' + i + ')" ' +
            'ondragover="dragOverCol(event,' + i + ')" ondragleave="dragLeaveCol(event)" ' +
            'ondrop="dropCol(event,' + i + ')" ondragend="dragEndCol(event)" ' +
            'title="拖动可调整列顺序">' +
            '<input draggable="false" value="' + esc(h) + '" onchange="setHeader(' + i + ', this.value)" ' +
            'placeholder="表头' + (i+1) + '">' +
            '<button class="del-col" onmousedown="event.stopPropagation()" onclick="event.stopPropagation(); delColumn(' + i + ')" ' +
            'title="删除此列">✕</button></th>';
  });
  html += '<th style="width:46px"></th></tr></thead><tbody>';
  (data.rows || []).forEach((row, r) => {
    html += '<tr><td class="row-op">' + (r+1) + '</td>';
    data.headers.forEach((_, c) => {
      const v = row[c] || '';
      html += '<td contenteditable="true" data-r="' + r + '" data-c="' + c + '" ' +
              'oninput="setCell(' + r + ',' + c + ', this.innerText)" ' +
              'data-empty="' + (v ? '' : '1') + '">' + esc(v) + '</td>';
    });
    html += '<td class="row-op"><button class="row-del" onclick="delRow(' + r + ')" title="删除此行">✕</button></td></tr>';
  });
  html += '</tbody></table>';
  html += '<button class="row-add-bar" onclick="addRow()">＋ 加一行（追加到末尾）</button>';
  wrap.innerHTML = html;
}

// 空单元格显示占位提示（CSS ::before 实现）
const style = document.createElement('style');
style.textContent = 'td[data-empty="1"]::before{content:"点击填写";color:#bbb;pointer-events:none;}';
document.head.appendChild(style);

function esc(s) {
  return String(s == null ? '' : s).replace(/&/g,'&amp;').replace(/</g,'&lt;')
         .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function setHeader(i, v) { data.headers[i] = v; }

/* ---------- 列拖拽换位置 ---------- */
let dragCol = null; // 正在拖动的列下标

function dragStartCol(e, i) {
  dragCol = i;
  e.dataTransfer.effectAllowed = 'move';
  e.target.classList.add('dragging');
}
function dragOverCol(e, i) {
  e.preventDefault();               // 允许放置
  e.dataTransfer.dropEffect = 'move';
  e.currentTarget.classList.add('drag-over');
}
function dragLeaveCol(e) {
  e.currentTarget.classList.remove('drag-over');
}
function dragEndCol(e) {
  e.target.classList.remove('dragging');
  dragCol = null;
  document.querySelectorAll('th.drag-over').forEach(t => t.classList.remove('drag-over'));
}
function dropCol(e, i) {
  e.preventDefault();
  e.currentTarget.classList.remove('drag-over');
  if (dragCol === null || dragCol === i) return;
  moveColumn(dragCol, i);
  dragCol = null;
}
function moveColumn(from, to) {
  // 表头和每一行数据同步移动
  const h = data.headers.splice(from, 1)[0];
  data.headers.splice(to, 0, h);
  data.rows.forEach(r => {
    const v = r.splice(from, 1)[0];
    r.splice(to, 0, v);
  });
  render();
}
function setCell(r, c, v) { while (data.rows[r].length < data.headers.length) data.rows[r].push(''); data.rows[r][c] = v; }
function addColumn() {
  if (!data.headers) data.headers = [];
  if (!data.rows) data.rows = [];
  data.headers.push('');
  data.rows.forEach(r => r.push(''));
  render();
}
function addRow() {
  if (!data.rows) data.rows = [];
  data.rows.push(new Array(data.headers.length).fill(''));
  render();
  // 滚动到底部，让新行可见
  const wrap = document.getElementById('wrap');
  wrap.scrollTop = wrap.scrollHeight;
}
function delRow(r) { if (confirm('删除第 ' + (r+1) + ' 行？')) { data.rows.splice(r, 1); render(); } }
function delColumn(c) {
  const name = data.headers[c] || ('第 ' + (c+1) + ' 列');
  if (confirm('删除列「' + name + '」？该列所有数据将一并删除。')) {
    data.headers.splice(c, 1);
    data.rows.forEach(r => r.splice(c, 1));
    render();
  }
}

async function save() {
  if (!currentId) return;
  // 只提交 headers/rows，后端按 id 更新对应表格
  const body = JSON.stringify({ headers: data.headers, rows: data.rows });
  const res = await fetch('/save?id=' + currentId, { method: 'POST',
    headers: {'Content-Type': 'application/json'}, body: body });
  const st = document.getElementById('status');
  st.textContent = res.ok ? '已保存 ✓' : '保存失败 ✗';
  st.className = 'show';
  setTimeout(() => st.classList.remove('show'), 2000);
  loadTables(); // 刷新行数角标
}
function exportCSV() { if (currentId) location.href = '/export?id=' + currentId; }

// 单元格内粘贴时按纯文本插入，支持多行
document.addEventListener('paste', e => {
  if (e.target.tagName === 'TD' && e.target.hasAttribute('contenteditable')) {
    e.preventDefault();
    const text = (e.clipboardData || window.clipboardData).getData('text/plain');
    document.execCommand('insertText', false, text);
  }
});

/* ---------- 键盘导航：Alt/Option + 方向键移动单元格 ---------- */
function cellAt(r, c) {
  return document.querySelector('td[contenteditable][data-r="' + r + '"][data-c="' + c + '"]');
}

// toEnd=true 光标落到末尾；false 落到开头
function focusCell(r, c, toEnd) {
  const td = cellAt(r, c);
  if (!td) return false;
  td.focus();
  const range = document.createRange();
  range.selectNodeContents(td);
  range.collapse(!toEnd);
  const sel = window.getSelection();
  sel.removeAllRanges();
  sel.addRange(range);
  return true;
}

// 返回 true 表示已处理（调用方需 preventDefault）
function handleCellArrow(e) {
  if (!e.altKey) return false;                       // 必须配合 Alt / Option
  if (e.metaKey || e.ctrlKey || e.shiftKey) return false;
  const td = e.target.closest ? e.target.closest('td[contenteditable]') : null;
  if (!td || !td.dataset.r) return false;

  const r = +td.dataset.r, c = +td.dataset.c;
  const nRows = (data.rows || []).length;
  const nCols = (data.headers || []).length;

  switch (e.key) {
    case 'ArrowUp':
      return r > 0 ? focusCell(r - 1, c, true) : true;   // 首行：就地停住
    case 'ArrowDown':
      if (r + 1 < nRows) return focusCell(r + 1, c, true);
      addRow();                                          // 已是最后一行 → 新增一行
      return focusCell(nRows, c, true);
    case 'ArrowLeft':
      if (c > 0) return focusCell(r, c - 1, true);
      return r > 0 ? focusCell(r - 1, nCols - 1, true) : true;
    case 'ArrowRight':
      if (c < nCols - 1) return focusCell(r, c + 1, false);
      if (r + 1 < nRows) return focusCell(r + 1, 0, false);
      addRow();
      return focusCell(nRows, 0, false);
  }
  return false;
}

/* ================= 视图切换 ================= */
function switchView(v) {
  document.querySelectorAll('.nav button').forEach(b =>
    b.classList.toggle('active', b.dataset.view === v));
  document.getElementById('view-tables').style.display = v === 'tables' ? '' : 'none';
  document.getElementById('view-memos').style.display = v === 'memos' ? 'flex' : 'none';
  if (v === 'memos') loadMemos();
  else if (memoTimer) { clearTimeout(memoTimer); memoTimer = null; saveMemo(true); }
}

/* ================= 备忘录 ================= */
let memos = [], curMemoId = null, memoTimer = null;

function fmtTime(ts) {
  const d = new Date(ts * 1000), p = n => String(n).padStart(2, '0');
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
         ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
}
function flash(elId, text) {
  const el = document.getElementById(elId);
  if (!el) return;
  el.textContent = text;
  el.classList.add('show');
  setTimeout(() => el.classList.remove('show'), 1800);
}

async function loadMemos(selectId) {
  memos = await (await fetch('/memos')).json();
  let id = selectId;
  if (!id) {
    if (memos.some(m => m.id === curMemoId)) id = curMemoId;
    else if (memos.length) id = memos[0].id;
  }
  renderMemoList();
  if (id) await selectMemo(id);
  else { curMemoId = null; showMemoEmpty(); }
}

function renderMemoList() {
  const el = document.getElementById('memo-list');
  if (!memos.length) { el.innerHTML = ''; return; }
  el.innerHTML = memos.map(m =>
    '<div class="memo-item' + (m.id === curMemoId ? ' active' : '') + '" onclick="selectMemo(\'' + m.id + '\')">' +
      '<div class="t">' + esc(m.title || '无标题备忘') + '</div>' +
      '<div class="p">' + esc(m.preview || '（空）') + '</div>' +
      '<div class="d">' + fmtTime(m.updatedAt) + '</div>' +
    '</div>').join('');
}

function showMemoEmpty() {
  document.getElementById('memo-main').innerHTML =
    '<div class="memo-empty">还没有备忘录，点左上角「＋ 新建备忘」开始记录</div>';
}

function editorHTML() {
  return '<input id="memo-title" placeholder="标题" oninput="onMemoInput()">' +
    '<div class="memo-meta"><span id="memo-time"></span><span id="memo-status"></span></div>' +
    '<textarea id="memo-body" placeholder="写点什么…（停止输入后自动保存）" oninput="onMemoInput()"></textarea>' +
    '<div class="memo-foot"><span style="font-size:12px;color:#bbb">自动保存 · Cmd/Ctrl+S 立即保存</span>' +
    '<button onclick="deleteMemo()">🗑 删除此备忘</button></div>';
}

async function selectMemo(id) {
  // 切换前先保存还没落盘的编辑
  if (memoTimer) { clearTimeout(memoTimer); memoTimer = null; await saveMemo(true); }
  const res = await fetch('/memo?id=' + id);
  if (!res.ok) { loadMemos(); return; }
  const m = await res.json();
  curMemoId = m.id;
  const main = document.getElementById('memo-main');
  main.innerHTML = editorHTML();
  document.getElementById('memo-title').value = m.title || '';
  document.getElementById('memo-body').value = m.content || '';
  document.getElementById('memo-time').textContent = '最后修改 ' + fmtTime(m.updatedAt);
  renderMemoList();
}

function onMemoInput() {
  if (memoTimer) clearTimeout(memoTimer);
  memoTimer = setTimeout(() => { memoTimer = null; saveMemo(true); }, 600);
}

async function saveMemo(silent) {
  if (!curMemoId) return;
  const titleEl = document.getElementById('memo-title');
  const bodyEl = document.getElementById('memo-body');
  if (!titleEl || !bodyEl) return;
  const res = await fetch('/memo?id=' + curMemoId, {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({ title: titleEl.value, content: bodyEl.value })
  });
  if (!res.ok) { flash('memo-status', '保存失败 ✗'); return; }
  const m = await res.json();
  const t = document.getElementById('memo-time');
  if (t) t.textContent = '最后修改 ' + fmtTime(m.updatedAt);
  if (!silent) flash('memo-status', '已保存 ✓');
  memos = await (await fetch('/memos')).json();
  renderMemoList();
}

async function newMemo() {
  const res = await fetch('/memos/op', { method: 'POST',
    headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ op: 'create' }) });
  if (!res.ok) { alert('新建失败'); return; }
  const m = await res.json();
  await loadMemos(m.id);
  const t = document.getElementById('memo-title');
  if (t) { t.focus(); t.select(); }
}

async function deleteMemo() {
  if (!curMemoId) return;
  const item = memos.find(m => m.id === curMemoId);
  const name = item ? (item.title || '无标题备忘') : '此备忘';
  if (!confirm('删除「' + name + '」？内容不可恢复。')) return;
  memoTimer && clearTimeout(memoTimer);
  memoTimer = null;
  await fetch('/memos/op', { method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({ op: 'delete', id: curMemoId }) });
  curMemoId = null;
  loadMemos();
}

/* ================= 快捷键面板 ================= */
// Mac 上 Ctrl 显示为 ⌘、Alt 显示为 Option，避免用户按错
const isMac = /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent || '');
const MOD_LABEL = isMac ? '⌘' : 'Ctrl';
const ALT_LABEL = isMac ? 'Option' : 'Alt';

function applyKeyLabels() {
  document.querySelectorAll('.mod-key').forEach(el => { el.textContent = MOD_LABEL; });
  document.querySelectorAll('.alt-key').forEach(el => { el.textContent = ALT_LABEL; });
}

function toggleKbd(show) {
  const modal = document.getElementById('kbd-modal');
  const on = (show === undefined) ? !modal.classList.contains('show') : !!show;
  modal.classList.toggle('show', on);
}

/* ================= 锁屏 ================= */
const LOCK_KEY = 'lov_locked';
const LEGACY_PIN_KEY = 'lov_lock_pin';   // 旧版本存在浏览器里的密码，升级后清理
let clockTimer = null;
let pinSet = false;                      // 服务端是否已设置密码

function startClock() {
  const tick = () => {
    const d = new Date(), p = n => String(n).padStart(2, '0');
    document.getElementById('lock-clock').textContent =
      p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
    const w = ['日', '一', '二', '三', '四', '五', '六'][d.getDay()];
    document.getElementById('lock-date').textContent =
      d.getFullYear() + ' 年 ' + (d.getMonth() + 1) + ' 月 ' + d.getDate() + ' 日 · 星期' + w;
  };
  tick();
  if (clockTimer) clearInterval(clockTimer);
  clockTimer = setInterval(tick, 1000);
}

async function lockNow() {
  const screen = document.getElementById('lock-screen');
  toggleKbd(false);                    // 锁屏时收起快捷键面板
  screen.classList.add('show');
  localStorage.setItem(LOCK_KEY, '1');
  localStorage.removeItem(LEGACY_PIN_KEY);
  startClock();

  try {
    pinSet = (await (await fetch('/pin')).json()).set;
  } catch (e) { pinSet = false; }

  document.getElementById('lock-pin-wrap').style.display = pinSet ? 'flex' : 'none';
  document.getElementById('lock-unlock').textContent = pinSet ? '解锁' : '点击解锁';
  document.getElementById('lock-set').textContent = pinSet ? '修改 / 清除密码' : '设置密码（可选）';
  document.getElementById('lock-err').textContent = '';
  const input = document.getElementById('lock-pin');
  input.value = '';
  if (pinSet) setTimeout(() => input.focus(), 60);
}

function unlockNow() {
  document.getElementById('lock-screen').classList.remove('show');
  localStorage.removeItem(LOCK_KEY);
  if (clockTimer) { clearInterval(clockTimer); clockTimer = null; }
}

async function tryUnlock() {
  if (!pinSet) { unlockNow(); return; }
  const input = document.getElementById('lock-pin');
  if (!input.value) { document.getElementById('lock-err').textContent = '请输入密码'; return; }
  let ok = false;
  try {
    const res = await fetch('/pin/verify', { method: 'POST',
      headers: {'Content-Type': 'application/json'}, body: JSON.stringify({ pin: input.value }) });
    ok = res.ok && (await res.json()).ok;
  } catch (e) { ok = false; }
  if (ok) { unlockNow(); return; }
  document.getElementById('lock-err').textContent = '密码错误，请重试';
  input.value = '';
  input.focus();
}

async function setPin() {
  const old = pinSet ? prompt('请输入当前密码：') : '';
  if (old === null) return;
  const np = prompt('设置新密码：4-6 位数字\n（留空并确定 = 清除密码，直接用点击解锁）', '');
  if (np === null) return;

  let body;
  if (np.trim() === '') {
    body = { op: 'clear', oldPin: old };
  } else {
    if (!/^\d{4,6}$/.test(np.trim())) { alert('请输入 4-6 位纯数字'); return; }
    body = { op: 'set', pin: np.trim(), oldPin: old };
  }
  const res = await fetch('/pin', { method: 'POST',
    headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body) });
  if (!res.ok) {
    alert(res.status === 403 ? '当前密码不正确' : '操作失败，请重试');
    return;
  }
  alert(body.op === 'set' ? '密码已设置，请牢记（忘了可用命令行重置）' : '已清除密码');
  lockNow();
}

function forgotPin() {
  alert('忘记密码的找回方法：\n\n' +
        '1. 打开终端，进入程序目录\n' +
        '2. 执行：./run.sh --reset-pin\n' +
        '3. 回到本页面，点「点击解锁」即可进入\n\n' +
        '重置只清除锁屏密码，表格和备忘录数据不受影响。');
}

// 锁屏状态下屏蔽页面快捷键
function isLocked() {
  return document.getElementById('lock-screen').classList.contains('show');
}

// Ctrl/Cmd + S 保存；Ctrl/Cmd + Shift + L 锁屏
document.addEventListener('keydown', e => {
  const mod = e.metaKey || e.ctrlKey;
  // 锁屏快捷键（Ctrl+L 被浏览器地址栏占用，所以加 Shift）
  if (mod && e.shiftKey && (e.key === 'l' || e.key === 'L')) {
    e.preventDefault();
    if (!isLocked()) lockNow();
    return;
  }
  if (isLocked()) {
    // 锁屏时仍然拦掉浏览器的保存网页等默认行为
    if (mod && e.key === 's') e.preventDefault();
    return;
  }
  // Esc 关闭快捷键面板
  if (e.key === 'Escape' && document.getElementById('kbd-modal').classList.contains('show')) {
    toggleKbd(false);
    return;
  }
  // ? 打开快捷键面板（输入状态下不拦截）
  if (e.key === '?') {
    const t = e.target;
    const typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable);
    if (!typing) { e.preventDefault(); toggleKbd(); return; }
  }
  // 表格内方向键导航（↓ 到底自动加行）
  if (e.key.indexOf('Arrow') === 0 && handleCellArrow(e)) {
    e.preventDefault();
    return;
  }
  if (mod && e.key === 's') {
    e.preventDefault();
    if (document.getElementById('view-memos').style.display === 'flex') saveMemo(false);
    else save();
  }
});

applyKeyLabels();
loadTables();

// 刷新后保持锁屏状态
if (localStorage.getItem(LOCK_KEY) === '1') lockNow();
</script>
</body>
</html>`
