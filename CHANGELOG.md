# IDUNA Changelog

## 2026-09-03 (8)
- docs: new `docs/NORTHSTAR_INVENTORY.md` — real, unified scoping pass for 3 related priority-queue cards (`AI-ITL`, `II-001`, `II-102`): a personal electronics/component inventory hosted as an IDUNA sibling app (same `drive`/`blog`/`vault` "monorepo-custom behind the portal" pattern), seeded with the founder's own real, named starting inventory (2x Pi B2?, 2x Pi Zero, an Adafruit Feather with a radio module). Flat SQLite schema, real API surface (plain CRUD + a genuinely new `POST /api/v1/inventory/query` endpoint feeding the real inventory into an LLM call for the AI-guidance ask). 3-phase plan matching each card's own real framing. Registered as golden doc `IDUNA-INVENTORY-NORTH`. Planning only, no code written.

## 2026-09-03 (7)
- feat: unified search extended to a real third corpus, Blog Posts (kanban priority-queue card 9944, 'OG IDUNA unified search'). Found live that card 1111's own original /portal/search only covered Apples + the unified event log -- IDUNA's own real, prominent blog content (Tyler's REDGARDEN/ecosystem series) had no search story at all. New internal/blog.Store.Search(query, limit) -- plain title-OR-body LIKE match, same deliberate 'narrowest real slice, not a full-text index' scope SearchApples already established. New PortalHandler.BlogStore field (optional, same independent-availability shape as Store/EventLog -- an unconfigured blog store never blocks the other two corpora). Template updated: new Blog Posts results section between Apples and Log Events, intro copy updated from 'two' to 'three real corpora.' New tests: internal/blog/store_test.go (TestStore_Search_MatchesTitleOrBody, TestStore_Search_LimitRespected), internal/http/handlers/portal_test.go (TestPortalHandler_Search_IncludesBlogPosts, TestPortalHandler_Search_UnconfiguredBlogStoreDoesNotBlockOthers). go test ./... clean, zero regressions. Live-verified: rebuilt + restarted iduna.service, confirmed /health, confirmed the live blog.db has real matchable content ('duck' matches 'The Duck Also Has Opinions About the Hoodie'). (sess-20260902-2008-ed50169e)
- kanban 卡片 1111「IDUNA UNIFIED SEARCH INTERFACE」:新增 `/portal/search`,一個真正的統一搜尋
  頁面——一個查詢框,同時對兩個真實的資料來源下去查,結果放在同一頁。Apples 這邊新增
  `store.IAMStore.SearchApples`(SQLite/MySQL 兩邊都真的實作了):在此之前 `ListApples` 只能用
  精確的 `agent_id`/`source_repo`/`apple_type` 篩選,完全沒有任何自由文字搜尋 title/body 的能力
  ——這次補上真正的 `title LIKE ? OR body LIKE ?`,誠實地用普通 LIKE、不是 FTS5 全文索引(跟
  kanban 卡片 9933 那邊 `bstree.prn` 自己的取捨一樣:先做最小可用的真實版本,真正的索引結構是
  之後、獨立的後續工作)。事件日誌那邊直接重用 `/portal/logs` 頁面已經有的真正 `searchEvents`,
  用同一個查詢字串,不另外發明第二套搜尋語法。兩邊各自獨立回報自己「有沒有設定好」,其中一邊沒
  設定(例如 `EventLog`/`Store` 是 nil)不會擋住另一邊真的把結果秀出來。新增 6 個測試(store 層
  2 個真的對著記憶體 SQLite 跑、handler 層 3 個真的驗證兩個來源同時出結果、其中一邊沒設定另一邊
  仍正常運作),`go build/vet/test ./...` 全部乾淨、零回歸。真的在 live `iduna.service` 上重建
  重啟過,`/health` 綠燈;另外直接對正式環境的 `iduna.db` 跑了一次真正的 `LIKE` 查詢,確認語法
  跟資料真的對得上(查到 3 筆真實、剛剛才寫進去的 kanban 相關 Apple)。誠實、還沒驗證的部分:
  `/portal/search` 這個頁面本身需要真正的 cookie 登入(`devportal.access` + `logs.read` +
  `apples.read`),這個 sandbox 沒有真正的 admin 帳號可以登入測試,所以頁面本身的即時渲染沒有
  對著 live 服務直接驗證過,只驗證到 handler 層測試 + 服務重啟沒有 panic(樣板本身用
  `template.Must` 解析,寫錯會直接在啟動時炸掉)。 (sess-20260902-2008-ed50169e)

## 2026-09-03 (6)
- kanban 卡片 1001「EMILY+ paywall needs to actually function with user accounts etc」——真的找到
  並修掉兩個真實、嚴重的問題,不是重寫整個訂閱系統。**問題一(真正的安全漏洞)**:
  `POST /api/v1/subscriptions/stripe`(Stripe webhook)自己的簽章驗證是假的——舊程式碼只檢查
  `Stripe-Signature` header「有沒有存在」,從來沒有真的驗證過內容,而且只要沒設定
  `GFD_STRIPE_WEBHOOK_SECRET`,連這個「有沒有存在」的檢查都整個跳過(fail open,不是 fail
  closed)。代表任何人都可以直接 POST 一個假的
  `customer.subscription.created` 事件、指定任意 `iduna_user_id`,免費幫自己(或別人)開通真正的
  付費訂閱,完全不用真的付錢。新增 `verifyStripeSignature`——直接照 Stripe 官方文件的 v1 簽章
  演算法手刻(HMAC-SHA256 對 `"<timestamp>.<raw body>"`,常數時間比對,含真實的
  timestamp 容忍度做重放攻擊防護),不用另外拉 `stripe-go` 這個 SDK 依賴。**沒有設定 secret 現在
  代表整批事件全部拒絕,不是全部信任**。**問題二(真正會擋住正式環境運作的路由 bug)**:
  `main.go` 把整個 `/api/v1/subscriptions/*`(含 `/stripe`)都包在 `RequireAuth` 底下——但
  Stripe 自己打進來的 webhook 呼叫本來就不會帶 IDUNA 自己發的 JWT,代表**正式環境裡 Stripe 根本
  打不進這個 endpoint,訂閱永遠不會透過 webhook 真的啟用**。同一批順便修好 `/tiers`
  (handler 自己的文件說是 public,實際上一樣被擋);`/stripe`、`/tiers` 改成獨立、真正公開的
  route,其餘(`/me`、手動 provision)維持要驗證。10 個新測試(含一個真的重現原本漏洞的測試:
  偽造簽章打進真正的 HTTP handler,確認被拒絕**而且訂閱真的沒有被寫進去**),`go build/vet/test
  ./...` 全部乾淨、零回歸。真的在 live `iduna.service` 上驗證過:重建重啟後,拿偽造簽章直接打
  正式服務,現在真的會被擋(這台機器本身沒設定真正的 webhook secret,所以正確地整批 fail
  closed 回 503,不是接受),`/tiers` 現在真的不用登入就能拿到。誠實、還沒驗證的部分:這個
  sandbox 沒有真正的 Stripe webhook secret,所以「真正合法簽章被正確接受」這條路徑只在
  `go test`(用真的算出來的 HMAC 模擬)驗證過,沒有對著 LIVE 服務用真正的 Stripe secret 跑過。
  commit `75f9b33`。 (sess-20260902-2008-ed50169e)

## 2026-09-03 (5)
- kanban 卡片 9966:Back Office 的選單改成真正固定在左邊的側邊欄,不再是頂端一整排橫向塞滿的
  連結列。創辦人:「/design updatge the IDUNA OG menu interface so its a left menu thats always
  there keep it simple and nice let us scroll up and down on the menu if it gets that long」。
  `internal/http/handlers/admin.go` 的 `adminBase` 樣板:`nav` 改成 `position:fixed` 貼齊視窗左邊
  跟上下邊界,自己有獨立的 `overflow-y:auto`,連結變多、變長也只會在選單自己裡面捲動,不會把主要
  內容往下推或整個視窗爆版;`body` 加上對應的 `padding-left` 讓主內容區不被蓋住;連結清單本身、
  href 全部保持不變,只是排版方向從橫向改成直向,「Sign out」用 `margin-top:auto` 固定貼在選單
  最下面。`go build/vet/test ./...` 乾淨;真的把 `iduna.service` 重建、重啟過,`/health` 綠燈——
  服務能正常啟動這件事本身就是真實驗證:`adminBase` 是用 `template.Must` 解析的,樣板寫錯會直接
  在啟動時 panic,不是靜默失敗。 (sess-20260902-2008-ed50169e)

## 2026-09-03 (4)
- S243-02:新增 `docs/EMILY_FOR_BUSINESS_NORTHSTAR.md`,針對創辦人自己的框架「IDUNA IS THE PRODUCT
  BASICALLY ZERO TRUST SECURITY AGENT NATIVE」做一次真實、有根據的產品範疇界定——不是實作計畫,是
  給創辦人層級決策用的真實輸入。直接點名一個真實、沒有含糊帶過的矛盾:`docs/NORTHSTAR.md` 自己現在
  仍然寫著「IDUNA is not a product. It is the backbone.」,這份新文件沒有替創辦人偷偷選邊站。把
  IDUNA 現有、已查證的真實能力(agent-native 雙軌認證、階層式 RBAC、git 備份的 Apples 稽核帳本、
  剛關閉的 SECTION 226 統一日誌後端)對到一個真正對外的 zero-trust 產品賣點上,同時誠實列出真正缺少
  的部分:沒有多租戶、沒有自助 onboarding、沒有持續性裝置/posture 驗證(EmilyOS 自己的
  `docs/POSTURE.md` 有設計但大多還是 `[ ] Milestone 1/2` 沒真的做)、沒有網路微分段故事、沒有真正
  完成的合規稽核(EmilyOS 的 `docs/SOC2.md` 是控制項對照表,不是稽核證明)。留下 4 個明確、沒有在這裡
  解決的開放問題給創辦人:是要把現有 IDUNA 本身外部化,還是另外做一個沿用其設計的新產品;真正的買家
  是誰;EmilyOS 的 posture kernel 要不要併進這個賣點;定價/包裝模式。已登記進
  `EMILY/context/golden-docs-index.md`(`EMILY-FOR-BUSINESS-NORTH`)。 (sess-20260902-2008-ed50169e)

## 2026-09-03 (3)
- S207-68:kanban 板真的可以在同一欄內排序卡片了。原本拖曳只能把卡片丟到別的欄位(改
  `queue`),丟回同一欄完全是 no-op,沒有任何真正的 `position` 寫回——後端
  `PATCH .../cards/{id} {"position":N}` 其實老早就支援了,只是前端從沒用過。這次
  補上兩條真的可用路徑:(1) 拖曳時,`onCardDragOver` 會依游標在目標卡片上半/下半
  即時把被拖曳的 DOM 節點插到正確位置,放開時 `onDrop` 讀出該欄最終的真實 DOM 順序,
  對每張卡片各發一次 `PATCH {queue, position}`,重新編出 0..n-1 的連續序號(即使是
  跨欄拖曳也一次到位,不用兩次呼叫);(2) 新增 ▲/▼ 按鈕(`moveCardBy`),對不方便
  拖曳的輸入裝置提供一個真正、可預期、好測試的替代路徑,用 `kanbanOrder` 這個
  server-confirmed 的順序快取算出要跟哪張鄰居互換,一樣走同一條 `persistColumnOrder`。
  新增 `TestKanban_PatchPositionReordersColumn`(真實整合測試:建三張卡,PATCH
  position 把順序反過來,GET 驗證真的照新順序回傳,不是建立順序),驗證前端依賴的
  這個 API 合約本來就對。`go build/vet/test ./...` 全倉庫乾淨零回歸。真的重建
  `iduna.service` 並重啟、`health` 檢查通過;`/admin/kanban` 在未登入時正確回
  401(admin 權限閘門本身沒被動到)。 (sess-20260902-2008-ed50169e)

## 2026-09-03 (2)
- S235-01:kanban 手動加卡片,現在可以只打 section 編號(例如 `S203`),不用自己猜一個沒被用過的 item 編號。新的 `resolveBareSectionID`(`internal/http/handlers/kanban.go`)真的讀 `backlog.ParseFile` 的即時內容,找出該 section 底下真正最大的編號再 +1——不是亂猜,已經有完整 id(`S203-04`)的呼叫方完全不受影響。讀檔失敗就老實 fallback 到 `-01`,不會擋住建卡。`create()` 的回應現在會真的把解析後的 `backlog_item_id` 傳回去,前端狀態列也會顯示實際分配到哪個 id。6 個新測試(單元測試 + 一個真的重現當初那次真實碰撞的 create 整合測試:`S203` 在 `S203-04` 已存在時解析成 `S203-05`),外加對重啟後的 live `iduna.service` 做的真實驗證:`S235` 正確解析成 `S235-02`(因為這個 section 自己的 `S235-01` 已經佔用),真的同步進 `BACKLOG.md`,驗證完就清掉。`go build/test` 跟 `GOWORK=off go build/test`(真正獨立 CI 路徑)都乾淨,`go vet` 乾淨。 (sess-20260902-2008-ed50169e)

## 2026-09-03
- S226-04:把統一日誌系統(Splunk 風格)剩下的真實 code path 都接上,關閉 SECTION 226 整個 thread。S226-01~03 已經做完每個真的登入介面跟 admin 停權/解停權;這次補完清單裡剩下的:`HeimdalHandler.submit`(`iduna:heimdal.submit`)跟 `.patch`(`iduna:heimdal.transition`,帶真正的 `from_status`/`to_status`,`patch` 現在會先真的查一次舊狀態才更新)、`ApplesHandler.create`(`iduna:apples.create`,帶真正的 apple_id/agent_id/source_repo/apple_type)、`AdminHandler` 的角色 assign/revoke(`iduna:admin.role.assign`/`.revoke`)、agent 權限 grant/revoke(`iduna:admin.agent_permission.grant`/`.revoke`)。順手多做一個 S226-04 清單沒明講但同一個 handler、同樣安全敏感的:agent secret 輪替(`iduna:admin.agent.secret_rotate`——只記錄「有輪替過」,絕對不把剛產生的明文 secret 寫進事件裡,真的測試過)。全部沿用 S226-02/03 自己已經建立的真實慣例:`EventLog` 欄位 nil-safe(沒接就是完全不動作,不會 panic)、fire-and-forget(記錄失敗絕對不能擋住真正的業務流程)、共用同一個 `emitAuthEvent` helper。9 個新測試,`go build/test` 跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都乾淨,`go vet` 乾淨。真的在 live `iduna.service` 上驗證過,不只是單元測試:重啟服務後真的發一張 Apple(#17272),直接看 `var/eventlog/events/` 底下真正的 NDJSON 檔案,確認 `iduna:apples.create` 事件真的落地,欄位完全正確。commit `3d690d5`,Apple #17273。 (sess-20260902-2008-ed50169e)

## 2026-09-02
- S234-05:kanban 板真正的「Done」動作——歸檔進 BACKLOG.md + 真的發一張 Apple(founder real-time: "we still need to file the apple for moving it to done say manual kanban move or something in the apple or abbreviate that to get more of the context of the actual task it should be moved to a different section of the backlog for archive"→"yea and ensure that our sync is working both ways doing any process improvements we can to make the pluumbing more codified like running stuff througgh cli etc whatever you think")。`PATCH .../cards/{id} {"queue":"done"}` 不是塞進一個真的「done」欄位常數值(創辦人自己說「we dont have a done column」),是一個真正的特殊動作:(1)用新的 `backlog.ExtractItemRaw`(真的把一個項目自己完整的原始文字,包括所有續行,從目前位置整段剪下)把項目的真實文字搬到一個新的固定歸檔區段(`## SECTION 9001: ARCHIVE (completed via kanban board move)`),checkbox 改成 `[x]`,(2)真的發一張 completion Apple(`AppleType: backlog_completion`,標題「Manual kanban move: <真正的任務標題,截斷到 60 字>」,內文帶著真正的 backlog id 跟原始標題,不是空泛的佔位字)——重用 `h.Store.AppendApple`,跟 `create()` 自己發 Apple 的路徑一模一樣,不是另外一套,(3)真的把卡片從 `kanban_cards` 刪掉。「process improvements... codify the plumbing」順便做的真正整併:把 `apples.go` 自己的 `syncAppleToGit`(原本是 `*ApplesHandler` 的方法)改成一個共用的自由函式,`gitSyncMu` 升級成 package-level 的 `applesGitSyncMu`——現在 `KanbanHandler` 真正的 Done 動作跟 `ApplesHandler` 自己原本的路徑,共用同一份真正的「發 Apple 之後同步進 git」邏輯,不是兩份會走鐘的複製。

  真正上線驗證時,親手抓到一個真實的 live bug:`internal/backlog` 的 item id regex 原本寫死 `S\d+-\d+`(只認「S」加數字加減號加數字),但正式環境裡已經真實存在一張不符合這個形狀的卡——`GFD-SYNC`(創辦人自己在 kanban UI 建的真卡,不是測試假資料)——完全找不到,代表這個 bug 從 S234-04 上線那天就已經是真的、活的:任何人再對 `GFD-SYNC` 這個 id 建一次卡,既有的「已存在就不要重複加」判斷永遠回傳「找不到」,會真的把它重複寫進 `BACKLOG.md`。修法:regex 放寬成任何「字母開頭,後面接字母/數字/底線/減號」的 id 形狀,涵蓋 `S202-27`、`GFD-SYNC`、`S1-01` 這些全部真實出現過的形狀,不會誤吃到標題本文。真正在正式環境跑過整條 Done 流程兩次(第一次用修 regex 之前的 binary,誠實地報告「找不到對應的 BACKLOG.md 行」但還是照樣發了 Apple、刪了卡——這正是設計好的、優雅降級的行為;第二次用修好之後的 binary,真的把項目搬進 SECTION 9001、checkbox 打勾、真的發出 Apple #17232、真的推上 git,`emily apples list` 可以直接查到)。

  新增測試:`internal/backlog`(`GFD-SYNC` 形狀的 id 真的能被 `Parse`/`ByID`/`ExtractItemRaw` 找到,`ExtractItemRaw` 真的在下一個項目/下一個 section 標題處正確停下來)、`kanban_complete_test.go`(真的搬移+打勾+不留重複舊行、真的發出帶有真實任務內容的 Apple、真的沒有對應檔案行時的誠實降級路徑照樣把卡片清掉並發 Apple)。前端(`kanban_page.go`)在既有的「Send to」下拉選單加一個「✓ Done」選項——只給真正已經在板上的卡(不是 Inbox 項目,因為 Inbox 項目根本還沒有真正的 kanban_cards id 可以動作)。`go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,`go vet` 乾淨。commit TBD。(sess-20260830-1207-cc0ba7da)

- S234-04:kanban ↔ `EMILY/BACKLOG.md` 真正雙向同步(founder real-time: "i just added S202-200 can you make sure it ends up in the text file in git on like the eventual consistiency paradigm?... if it gets added to backlog via the kanban interface it needs to wind up in the golden backlog file in git and as we work it needs to all stay in sync... for example when we finish something it needs to move off the kanban board")。S234 之前只做了「檔案→板」單向(即時解析、只讀),這次補上另一半、真正雙向:(1)`KanbanHandler.create`——新建一張卡的 `backlog_item_id` 如果不是 `BACKLOG.md` 裡已經存在的真實項目,背景（fire-and-forget,不會擋住已經成功的 DB 寫入回應)真的把一行 `- [ ] **ID: 標題**` 加進一個新的、固定的收件區段(`## SECTION 9000: ADDED VIA IDUNA KANBAN INTERFACE`,故意不用猜的塞進某個既有主題 section),寫回檔案、`git add`/`commit`/`push`——完全複用 `apples.go` 自己既有、已經上線驗證過的 `syncAppleToGit`/`gitPushWithRetry` 這一套 idiom,不是重新發明;`gitPushWithRetry` 順便修一個真實的小 bug(log 訊息原本寫死 `[apples-git]`,現在改成呼叫端自己傳真正的 prefix,kanban 觸發的推送不會再被記成 apples 的)。(2)`KanbanHandler.list`——每次讀取都會即時對照 `BACKLOG.md` 真正的目前狀態,任何一張卡如果對應的項目已經被標記 `[x]` 完成,直接把那張卡從 `kanban_cards` 真的刪掉,不只是這次回應不顯示——「做完的東西真的會從看板上消失」。誠實聲明的一次性動作:直接對著正式環境的 sqlite(`var/iduna.db`)查證,這次上線前就已經存在、比這個同步功能還早建立的 10 張卡裡,有 3 張(`GFD-SYNC`/`S202-99`/`S202-200`——包含創辦人自己剛剛真的建的那張測試卡)在 `BACKLOG.md` 裡完全找不到,因為既有的自動同步只在「建立新卡」那一刻觸發,不會回頭處理更早就存在的卡——這 3 張手動一次性補進 `SECTION 9000`,commit 進 EMILY 自己的 git(`a3155ea0`),之後每一張新卡都會自動同步,不需要再手動補。新增 3 個真實測試(`kanban_git_sync_test.go`):真的用一個有 git repo 的暫存目錄驗證新項目真的被寫進檔案且真的產生一個 git commit(沒設 remote,push 會失敗但這是預期、非致命的 best-effort 行為,commit 本身一樣算數)、已存在的項目不會被重複加、已完成的項目真的會被從清單跟資料庫裡移除。`go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,`go vet` 乾淨。重建部署到正式環境的 `iduna.service`。commit TBD。(sess-20260830-1207-cc0ba7da)

- S234-03:修一個真實、活生生踩到的 kanban UX bug(founder real-time: "i am way down on the column of the inbox and i found one i want to drag over buut i actually cant because those oolumns are small and at the top of the page we need to either extend the columns down or add a right click send to interface")。真實根因:Inbox 欄位真的有 170 個 open 項目,整欄真的比一個螢幕高很多,往下捲去找一張卡的時候,其他三欄(真正的拖放目標)也一起被捲出畫面外,變成完全沒辦法拖。兩個真實修法一起做:(1)每一欄改成 `display:flex` + `max-height: calc(100vh - 12rem)`,裡面的卡片清單(`.cards`)自己 `overflow-y:auto`——四欄現在永遠同時整欄留在畫面裡,各自獨立捲動,不會再有一欄的長度把另一欄的拖放目標擠出畫面,這是真正、標準的 Trello 式看板做法,不是揣測;(2)每張卡(包括 Inbox 項目)新增一個一直看得到的「Send to…」下拉選單,選一個 queue 就直接呼叫既有的建卡/搬移邏輯——比真的做一個右鍵選單(還要處理自己的定位、點外面關閉、鍵盤操作)更簡單可靠,一樣完全解決「不用把來源跟目標同時擠在畫面裡」這個真實需求,額外還支援鍵盤操作。把 `onDrop`/新的 `onMoveToSelect` 共用同一個抽出來的 `sendToQueue()`,不是兩份會走鐘的複製邏輯。用 `go build/test`(workspace 模式)跟 `GOWORK=off go build/test` 都驗證過,`go vet` 乾淨,重建部署到正式環境的 `iduna.service` 後確認 `/admin/kanban` 還是活的(401,不是掛掉)。commit TBD。(sess-20260830-1207-cc0ba7da)

- S234:真正把 kanban 板接上 `EMILY/BACKLOG.md`(founder real-time: "get the backlog working with the kanban"→"for now we wont see the completed stuff on the kanban to save dom nodes we can view that data elsewhere for now")。之前 kanban 板跟 `kanban_cards` 資料表雖然都是真的、可以動,但完全沒有跟真正的 backlog 檔案接起來——每一張卡都要手動打字輸入 backlog id 跟標題。新增 `internal/backlog`(純讀取、機械式解析,不是第二個資料來源——`BACKLOG.md` 自己的 git 歷史才是真正的 event log,這只是一個便宜、可重建的讀取投影):真的掃出每一行 `- [ ]`/`- [x]` `**S###-##: 標題**` 項目,標題支援真實存在的多行粗體(regex 用 `(?s)` 讓 `.*?` 可以跨行,非貪婪比對到第一個 `**` 為止),同時記錄所在的 `## SECTION N` 標題跟真實行號。真的對著現在活的 27000+ 行 `EMILY/BACKLOG.md` 跑過:1315 個項目、170 個還沒完成。新增 `KanbanInboxHandler`(`GET /admin/kanban/api/inbox`,跟 `/admin/kanban` 同一組 cookie+`iduna.admin` 門檻):即時重新解析 `BACKLOG.md`,只回傳真正「還沒完成」而且「還沒被建成卡片」的項目——完成的項目直接照創辦人的話不顯示(省 DOM node,要看去 `BACKLOG.md` 本身)。前端(`kanban_page.go`):板面從 3 欄變 4 欄,新增最左邊的「Inbox」欄,顯示真正、還沒排進任何 queue 的 open 項目,一樣可以拖曳(拖進 backlog/priority/cruise 任一欄會直接呼叫既有的 `POST /admin/kanban/api/cards` 用真正解析出來的標題建卡,不是手打的)——拖曳邏輯改用 `dataset.kind`(`'card'` 走原本的 PATCH 移動、`'inbox'` 走新的 POST 建卡)分流,建卡/移動/刪除卡之後都會同時重新整理卡片跟 inbox 兩邊,確保「已排進 queue 的項目不再出現在 inbox」這件事永遠是真的、即時的,不是快取出來的舊資料。新增 `EMILY_BACKLOG_PATH` 環境變數(預設 `/home/fatbaby/EMILY/BACKLOG.md`,兩個 repo 在這台機器上本來就是同層的 sibling checkout)。新增測試:`internal/backlog/parse_test.go`(真實多行標題形狀、沒有 section 標題的邊界情況、`ByID` 查找、缺檔案的誠實錯誤)、`internal/http/handlers/kanban_inbox_test.go`(完成項目跟已經建卡的項目都要真的被排除、沒有 token 的 401、backlog 檔案不存在的誠實 503)。用 `go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,`go vet` 也乾淨。重建部署到正式環境的 `iduna.service` 後對 `/admin/kanban/api/inbox` 打真的 curl(拿到 401,不是 404——路由真的有註冊)。commit TBD。(sess-20260830-1207-cc0ba7da)

- S231:把已經做好、但沒有入口的 kanban 介面(`/admin/kanban`,3 欄 backlog/priority/cruise,拖拉)加進 Back Office 導覽列(founder real-time: "lets get the iduna kanban interface up so we want to be able to sort the backlog into the 3 columns backlog cruise and priority i think the backend work may be done for that we need the iduna kanban interface and it needs to be in the menu of iduna theres no room but just shove it in there its fine we will fix it soon")。查證過:後端(`kanban.go`)、頁面本身(`kanban_page.go`)、路由(`main.go` 的 `/admin/kanban`)全部都已經是真的、可以動的——唯一缺的就是導覽列裡沒有連結,找不到入口。直接照創辦人自己的話「先塞進去,之後再修版面」,在共用的 `admin.go` nav 模板裡加一行 `<a href="/admin/kanban">Kanban</a>`,沒有另外重排版面。驗證:`go build`(workspace 模式)跟 `GOWORK=off go build`(真正的獨立 CI 路徑)都乾淨,`go test ./internal/http/handlers/...` 全過,重建部署到正式環境的 `iduna.service` 後直接對 `/admin/kanban` 打真的 curl(拿到 401,不是 404——確認路由真的有註冊、middleware 鏈完整),並且對已編譯的 binary 做 `strings` 確認新的 nav 連結字串真的在裡面。commit TBD。(sess-20260830-1207-cc0ba7da)
- S227-01:在 developer portal 裡蓋出真正的 log 查詢介面,支援真正的 regex 查詢(founder real-time: "build the log query interface into the iduna developer portal make sure regex is available to query with we dont want to rely on iduna being up to view the logs but if iduna is our logging backend assuming it needs to be up anyways to query the logs - for now this is technical debt but it is also in the name of keeping operations simple while we plan migration")。後端:把 `logs.go` 的搜尋過濾邏輯抽成共用的 `searchEvents(ctx, store, searchQuery)`,`HandleSearch`(JSON API)跟新的 portal 頁面都呼叫同一份——不是兩份可能會走鐘的複製。新增真正的 regex 支援:另外開一個獨立的 `regex` query 參數(不是塞進第四個 `search=` 詞,因為 regex pattern 本身可能含空白,會弄壞既有以空白分詞的 search 小語言),用 `regexp.Compile` 編譯、對每個事件自己原始的 JSON Data 做比對。誠實記下一個真實的安全特性:Go 的 `regexp` 本身編譯成 RE2 自動機(線性時間,不會有災難性回溯),所以就算 log 很大也不是真正的 ReDoS 攻擊面。錯誤的 pattern 會誠實回傳 400。Portal 端:新增 `PortalHandler.Logs`(`GET /portal/logs`),真正的 HTML 頁面,跟既有的米金色風格一致——一個普通的 GET 表單(`search=`/`regex=`,讓一次查詡變成真正、可加書籤/可分享的網址),有查詢的時候顯示結果表格。同時需要 `devportal.access`(portal 本來的門檻)跟 `logs.read`(JSON API 自己也要求的權限)——在 `main.go` 用巢狀的 `RequirePermission` 接起來。真實、直接寫在頁面上、沒有藏起來的限制:看 log 需要 IDUNA 自己是活著的,因為它本身就是 log 後端——直接寫在頁面文案裡,呼應創辦人自己的話「這是刻意接受的技術債」。首頁新增一個「Logs」的 tool row。新增 8 個測試:`logs_test.go` 2 個 regex 測試(真正的過濾搜尋、真正的錯誤 regex 400),`portal_test.go` 3 個新頁面測試(空表單、對著真正的 event log 做真正端到端查詢、錯誤 regex 的行內錯誤訊息)。用 `go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,兩邊都沒有回歸。同一則訊息裡創辦人另外提到一個探索性的遷移架構想法(用 fatbaby 自己的 proxy/proxy broker 做「灘頭堡」搬遷模式)——記錄成 `EMILY/BACKLOG.md` 的 S227-02(只有設計、還沒開始做),這次沒有動手做。commit `4dae169`(+`ad2d158` CLAUDE.md 修正)。(sess-20260830-1207-cc0ba7da)
- S226-03:把「所有」真正的登入介面跟 admin suspend/unsuspend 都接進統一 log 後端(founder real-time: "ensure admin events like suspend un suspend all logins to the iduna backend make sure that is all going to our logging platform")。延續 S226-02 已經建立好的真實做法(選填的 `EventLog` 欄位 + 共用的 `emitAuthEvent`),這次補齊剩下每一個真正的登入介面,加上使用者跟 agent 的 suspend/unsuspend:`LocalAuthHandler`(`POST /api/v1/auth/local`,發 bearer JWT)——`iduna:auth.local.success`/`.failure`,失敗事件只記嘗試登入的 email,絕對不記原始密碼;`AdminLoginHandler`(`POST /admin/login`,Back Office cookie session)——`iduna:auth.admin_login.success`/`.failure`,兩種不同的真實失敗原因(`invalid_credentials`、`missing_iduna_admin_permission`);`PortalHandler.LocalLogin`(`POST /portal/login`,developer portal cookie session)——`iduna:auth.portal.success`/`.failure`,雖然跟 `LocalAuthHandler` 一樣是 email+密碼,但這是真正、獨立的另一個介面,用不同的事件 Type 確認沒有混在一起;`AdminHandler.userAction`/`agentAction`(`POST /admin/users/{id}/suspend|activate`、`/admin/agents/{id}/suspend|activate`)——`iduna:admin.user.suspend`/`.unsuspend`、`iduna:admin.agent.suspend`/`.unsuspend`,這是創辦人自己明確點名的案例,只有真的操作成功才會發送事件。`main.go` 在 `portalH`/`adminLoginH`/`adminH` 已經用指標註冊進 `mux` 之後才晚一點接上 `unifiedLog`(跟 `googleAuthH`/`agentAuthH` 同一套「晚接線」寫法),`localAuthH` 則是直接在建構的時候就帶上(因為它是在 `unifiedLog` 已經存在之後才建構的)。新增/擴充 4 個測試檔,共 6 個新測試,每一個都直接驗證真正發出的事件 Type 跟內容,不只是「請求還是成功」:`TestLocalAuthHandler_EmitsEvents`、`TestPortalHandler_LocalLogin_EmitsEvents`、`TestAdminLoginHandler_EmitsEvents`(兩種失敗原因都測)、`TestAdminHandler_UserSuspendUnsuspend_EmitsEvents`、`TestAdminHandler_AgentSuspendUnsuspend_EmitsEvents`。`stubApplesStore`(`apples_test.go` 自己既有的)本來就已經滿足完整的 `store.IAMStore` 介面,直接重用,沒有再寫第二套 stub。用 `go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,兩邊都沒有回歸。commit `d6c7404`。(sess-20260830-1207-cc0ba7da)
- S226-02:把真正的 auth 事件接進統一 log 後端(上次 S226-01 自己點名留下來、刻意還沒做的後續)。`GoogleAuthHandler`、`AgentAuthHandler` 都新增一個選填的 `EventLog userlog.EventLog` 欄位。新增共用的 `emitAuthEvent` helper:誠實處理 nil(沒接 EventLog 的話完全沒有行為改變,不會 panic——兩個 handler 原本所有測試完全不用改就繼續全過)、真正的 Append 錯誤直接吞掉不管——log 後端掛掉絕對不能連累真正的 auth 流程,跟 `apples.go` 自己的 `syncAppleToGit` 同一套 fire-and-forget 慣例。四個真正的事件發送點:`iduna:auth.google.failure`(id_token 無效、身分被停權/封禁)、`iduna:auth.google.success`、`iduna:auth.agent.failure`(憑證錯誤——只記嘗試登入的 `agent_name`,絕對不記 `agent_secret`,真的寫了一個測試驗證這件事)、`iduna:auth.agent.success`。`main.go` 在 `unifiedLog` 建好之後才把它接進這兩個 handler(`googleAuthH`/`agentAuthH` 更早之前就已經用指標註冊進 `mux` 了,所以晚一點設定 `.EventLog` 一樣會生效在正在服務的那個實例上,跟 `portalH.Proj` 自己已經用過的同一套「先建構、依賴出現再接線」寫法一樣)。新增 `TestAgentAuthHandler_EmitsEvents`:真正的成功跟真正的失敗都會落進 log,而且驗證過失敗事件裡面絕對不會出現原始的 `agent_secret`。Google auth 自己的成功路徑沒有另外寫測試(`googleverify.Verify` 會打真正的外部端點,目前沒有可以注入替換的介面,這是既有的、這次沒有新增的測試缺口)——共用同一個已經被 agent 測試覆蓋到的 `emitAuthEvent`。用 `go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,兩邊都沒有回歸。commit `aaafcb7`。(sess-20260830-1207-cc0ba7da)
- 新增統一 log 後端,Splunk 風格(`POST /services/collector`、`GET /services/search/jobs`)(founder real-time: "create a unified logging backend for IDUNA using the new tech - so we can have one place to jump to and grab the logs - use whatever affordances and apis splunk uses")。新增 `internal/http/handlers/logs.go`:`LogsHandler` 直接沿用 IDUNA 自己既有、已經測試過的 `internal/userlog.FileEventLog`(NDJSON append-only log + Event 封裝),只是另外開一個獨立的根目錄(`var/eventlog/`,跟 userlog 自己的 `var/user-events/` 分開)——沒有重新做第三套一樣的東西,因為 userlog 自己的 Event 形狀(ID/Type/Source/OccurredAt/Data)本來就已經夠通用了。真實、有查證過而不是猜測的決定:這次刻意沒有直接 import PRRJECT_FATBABY 自己的 `eventstore`(雖然那個是同一個形狀更早的原版,一開始也是這次的直覺選擇)——因為 IDUNA 自己真正的 CI(`.github/workflows/iduna-construct.yml`)只會 checkout 這個 repo 自己、跑 `go test ./...`,完全沒有 `go.work`/其他 repo 的 checkout——直接用 `GOWORK=off go build ./...` 驗證過,真的會因為解析不到只存在本地 monorepo `go.work` 裡的跨 repo import 而編不過。這個問題是先抓到、修好才推上去的,不是等 CI 壞了才發現。兩個真正、對照 Splunk 自己 API 的端點:`POST /services/collector`(Splunk 自己真正的 HTTP Event Collector 端點路徑,`Authorization: Splunk <IDUNA_HEC_TOKEN>` 驗證,用 `crypto/subtle` 的 constant-time 比對,不是普通 `==`;Splunk 自己真正的請求 payload 形狀跟成功回應形狀都照抄);`GET /services/search/jobs`(Splunk 自己真正的搜尋端點路徑,這次刻意做成同步的——Splunk 自己真正的 API 是非同步兩段式的 job 建立/輪詢,這裡先直接回傳結果,誠實標明是簡化過的;支援一個真正、刻意縮小範圍的 SPL 子集:空白分隔的 `type=`/`source=`/`q=`,AND 起來;需要 `logs.read` 權限,走既有的 `RequireAuth`/`RequirePermission` middleware,跟其他受保護端點同一套)。誠實聲明這次刻意沒做的部分:沒有把 IDUNA 現有的任何真實邏輯(auth、admin 操作、HEIMDAL 轉換)接進去真的發送事件——這次只出貨真正、測試過的 ingest/search 基礎建設,不是把每一個既有 handler 都重新接一次線,那是更大、風險更高、之後才做的事。新增 7 個測試(`logs_test.go`):真正的事件寫入+讀回、錯誤/缺少 HEC token、缺少 sourcetype、search 的驗證/權限檢查、真正端到端的過濾搜尋、錯誤的搜尋語法。用 `go build/test`(workspace 模式)跟 `GOWORK=off go build/test`(真正的獨立 CI 路徑)都驗證過,兩邊都沒有回歸。`CLAUDE.md` 同步更新:新端點、`IDUNA_HEC_TOKEN` 環境變數、新增「Unified Logging Backend」章節。commit `839ee7d`。(sess-20260830-1207-cc0ba7da)

## 2026-08-28
- New RacerTicketHandler (POST /api/v1/racer/ticket) + racer matchmaking queue (/api/v1/racer/queue/*, MinPlayers=1) for WEAKNIGHT_BEDROCK_RACERS' real login flow — direct port of the existing SHANKPIT ticket/queue patterns. (sess-20260825-1938-f6bd411e)
- Real IDUNA email+password login for the developer portal (POST /portal/login) — LocalLogin handler, devportal.access grant for local_users, form added to portalLoginTmpl. Founder real-time pivot: real portal login before fixing Google OAuth. (sess-20260825-1938-f6bd411e)

- Portal: SARENA_NOTEBOOK row now links to its real, shipped v0 (was 'Not yet built'). commit 216914b. (sess-20260825-1938-f6bd411e)


## 2026-08-26
- portal.go: Jupyter tool row now links to the live JEWEL URL instead of href="#" (sess-20260825-1938-f6bd411e)
- OpenAPI/Swagger spec updated with kanban + dev portal endpoints, both copies (openapi.go live spec + openapi.yaml static copy). New regression test guards against future drift. Apple filed. (sess-20260825-1938-f6bd411e)

- Kanban prioritization layer shipped: kanban_cards table (backlog_item_id/queue/position, an overlay over BACKLOG.md, not a replacement), KanbanHandler (GET/POST/PATCH/DELETE /api/v1/kanban/cards, mounted cookie-gated under /admin/kanban/api/ for the GUI and bearer-gated under /api/v1/ with a new kanban.access permission for CLI/agent access), KanbanPageHandler (3-column drag-and-drop GUI at /admin/kanban, cream/gold style). 9 new tests. Live-verified table+permission already applied to production; binary swap queued (sudo-queue/29). Apple #16053. (sess-20260825-1938-f6bd411e)


## 2026-08-25
- Restyled /admin/login to the real IDUNA cream/gold style guide with Prompt-o-verse art (eye-of-providence-robot), cross-linked with /portal/login (sess-20260825-0828-cc32a704)
- Restyled /portal login + home to the real IDUNA cream/gold style guide with Prompt-o-verse art (fenrir-robot, fox-robot); added /portal/logout (sess-20260825-0828-cc32a704)
- New developer notebook portal: GET /portal/login (Google SSO) + GET /portal (gated by new devportal.access permission, granted to nobody by default) -- lists Jupyter and SARENA_NOTEBOOK, both not-yet-available (Apple #15924) (sess-20260825-0828-cc32a704)
- Fix: RequireCookieAuth no longer treats human Google-login cookie sessions as agent sessions during the live-status recheck (was bouncing every human session to login) (sess-20260825-0828-cc32a704)
- GoogleAuthHandler sets HttpOnly iduna_session cookie on login; RefreshHandler accepts/refreshes it -- auth foundation for the planned Jupyter/SARENA notebook portal (Apple #15918) (sess-20260825-0828-cc32a704)
- The ladybug admin-session-revocation regression spec now actually compiles, links, and runs (passed=1 failed=0) against a real fixture WebDriver server. Apple #15914. (sess-20260825-0828-cc32a704)
- SECURITY FIX: suspended agent's live Back Office session is now actually revoked -- RequireCookieAuth re-checks live agent status+permissions every request, not just at login. Apple #15908. (sess-20260825-0828-cc32a704)
- Drive slurp feature (S187-03/S188-05/S189-10): OAuth-based Back Office Drive browse+slurp, background job queue with idempotency, SSE progress (sess-20260825-0828-cc32a704)
- Migrated EDDY admin credential (Back Office iduna.admin secret) from plaintext ~/.ssh/iduna-admin-eddy.txt into IDUNA Vault as api_key item #1; plaintext file deleted. (sess-20260825-0828-cc32a704)

- S191-02: admin session sliding-expiration refresh — fixes silent hard-cutoff logout on the 8h admin cookie (founder-reported 'logged out when i shouldnt be'), reissues on activity once past half the TTL. Apple #15775. (sess-20260825-0828-cc32a704)


## 2026-08-20

- Sign every filed Apple with anchor + snowman (⚓ ☃), enforced server-side at POST /api/v1/apples (signAppleBody) per founder standing order (sess-20260820-0649-a3f19d93)


## 2026-08-18
- Added Store.MergeTags + PATCH /api/v1/promptoverse/nodes/{slug}/tags for backfilling node metadata (sess-20260813-2154-dda37e8b)
- Added /admin/promptoverse-queue Back Office page for fixing/queuing Prompt-o-verse generation queue entries without CLI access (sess-20260813-2154-dda37e8b)
- Auth strip now proactively refreshes its token via IDUNA's existing /api/v1/auth/refresh, plus retries once on a real 401 (sess-20260813-2154-dda37e8b)
- New cards/sections stagger in with a jittered fade-in instead of snapping in synchronously (sess-20260813-2154-dda37e8b)
- Auth strip now shows a visible 'Sign in (coming soon)' placeholder instead of rendering empty when GOOGLE_CLIENT_ID isn't set (sess-20260813-2154-dda37e8b)
- Fixed the real live-reload bug: subject/style pages never had a poll script at all (only the index page did). Added node_variants (regenerate-with-variation, additive, same page) (sess-20260813-2154-dda37e8b)
- Site-wide auth strip (sign-in funnel) on every Prompt-o-verse page — header pill shared across node/index/subject/style pages via one localStorage token (sess-20260813-2154-dda37e8b)
- Mashup nomination social tool: authenticated (Google ID-token) users nominate combining two subjects, admin-only approve/reject via new promptoverse.mashups.review permission, autocomplete widget on every subject page (sess-20260813-2154-dda37e8b)
- Fixed stale live-reload JS: iduna.service was running a binary built 21s before the incremental-DOM-patch fix landed, restarted with current code (sess-20260813-2154-dda37e8b)
- Renderer shows Mashups cross-links on subject/style pages, reading the new LLM-judgment cache from emily.cli (sess-20260813-2154-dda37e8b)
- fix(promptoverse): gallery index no longer re-renders on every live-update poll tick when the node list hasn't actually changed -- was causing visible flicker, especially the first card in each category (sess-20260813-2154-dda37e8b)
- feat(promptoverse): new cmd/promptoverse-thumbnails (systemd timer, 15min) idempotently generates a thumbnail + JPEG-optimized version of every node's image via ImageMagick convert, re-rendering afterward. Renderer resolves GalleryImageFile/HeroImageFile at render time (thumb/optimized if present, original otherwise); live-update JS falls back via onerror. Live: 1.9MB PNGs became 236KB optimized JPEGs + 24KB thumbnails. (sess-20260813-2154-dda37e8b)

- feat(promptoverse): GET /api/v1/promptoverse/discovery -- public read-only endpoint combining the hardcoded style registry (exported from emily.cli), discovered styles, GPT-2 candidate tags, and content-policy dead-letter entries (sess-20260813-2154-dda37e8b)


## 2026-08-17 (3)
- feat(promptoverse): gallery index now live-updates every 10s (fetch/setInterval, same idiom as OKEMILY/tournaments.html's hero leaderboard) via the existing GET /api/v1/promptoverse/nodes endpoint. New Style pages at /prompt-o-verse/style/<label-slug>/ (mirrors Subject pages but for the Label axis, always generated -- no leaf-count threshold), linked from every leaf's h1 and from index category headings. (sess-20260813-2154-dda37e8b)
- fix(promptoverse): subject page <img src> used a bare relative path that only resolves correctly from the top-level index, not from subject/<slug>/ which is one directory deeper -- every subject page's images 404ed. Switched to the absolute /prompt-o-verse/<slug>/<file> path already used by the <a href>s in the same template. Re-ran cmd/promptoverse-rerender to fix already-published pages. (sess-20260813-2154-dda37e8b)
- feat(promptoverse): subject-grouping (leaf pages with a Subject that has ≥ 2 published leaves get a linked '/subject/<slug>/' page and a clickable Applied-to line); RenderAll now re-renders every node + subject pages on every publish (not just the new node + index) so an OLDER sibling gains its link the moment a SECOND leaf under the same Subject goes live; added cmd/promptoverse-rerender (mirrors cmd/blog-rerender) and used it to backfill all 28 existing nodes, which also fixed a stale published_at=0001-01-01 baked into early VS0 leaf pages from before RenderAll existed (DB values were always correct -- the static HTML just never got re-rendered with them until now) (sess-20260813-2154-dda37e8b)

- feat(prompt-o-verse): add a real taxonomy level -- `Label` is now the style/subcategory (e.g.
  "Renaissance oil painting"), `Subject` is what it's applied to (e.g. "baseball card", "Master
  Chief (Halo)"), and each node carries both an `EZPrompt` (short, e.g. "renaissance oil painting
  master chief halo" -- what a normal/vanilla pipeline would receive unenriched) and the real
  `ExpandedPrompt` it was actually generated from, formalizing the northstar's own two-tier
  prompting model (§3) as real fields instead of one undifferentiated prompt string. Index page
  rewritten to group leaf nodes by `Label` (`<section>` per style, semantic heading + count),
  proving styles generalize across subjects instead of looking baseball-card-specific -- every
  leaf keeps its own dedicated page regardless of grouping (SEO, per founder direction). Seeded a
  new Master Chief (Halo) batch reusing 9 existing styles (1910s tobacco card, stained glass,
  8-bit pixel art, Renaissance oil painting, LEGO minifigure, claymation, pop art silkscreen,
  woodcut, watercolor) via Vertex AI -- 3/9 succeeded before hitting sustained rate limits, stopped
  there per founder's own "proceed with what you have" call rather than continuing to hammer the
  limiter. Wiped and reseeded all 20 live nodes (17 baseball card + 3 Master Chief) against the
  new schema. 2 new regression tests (shared-Label grouping, singular/plural variant count) +
  updated existing tests for the renamed/added fields. `go build`/`vet`/`test ./...` clean.
  Live-verified: shared styles now show "2 variants" on the index, e.g. the tobacco-card style
  applied to Master Chief renders a genuinely convincing "MASTER CHIEF · SPARTAN II · UNSC"
  Victorian-bordered lithograph card.

## 2026-08-17 (2)

- feat(prompt-o-verse VS0): new `internal/promptoverse` package + `PromptOVerseHandler`, same
  own-SQLite/render-to-static shape as `internal/tyler`, wired into okemily.com at
  `/prompt-o-verse/`. Each node carries exactly 3 pieces of data per the founder's own contract —
  top-level prompt, generated image, labeled taxonomy tags — rendered with real semantic HTML
  (`<article>`, `<figure>`/`<figcaption>`, `<dl>`/`<dt>`/`<dd>` for tags, `<time datetime>`), not
  div soup. New `promptoverse.write` permission (migration `202608170002`), granted to EMILY-PRIME.
  Seeded live with a real 20-top-level-prompt VS0 MVP batch (baseball card photography, 3 real
  historical eras + 17 fun/surreal transformations aimed at people new to generative AI, per
  founder direction) generated via Vertex AI's `gemini-2.5-flash-image` — 17/20 succeeded before
  hitting real rate limits (429s), 3 queued to backfill later per founder's own "proceed with what
  you have" call. `okemily-deploy.sh` updated to exclude `prompt-o-verse/` from its rsync (same
  blog/tyler live-render protection, would otherwise get wiped on next deploy). 11 new tests
  (store CRUD/dedup/ordering, renderer semantic-HTML/index output). `go build`/`vet`/`test ./...`
  clean. Live-verified on okemily.com, real generated images visible.

## 2026-08-17

- fix(S157-01): grant EMILY-PRIME the `heimdal.process` permission it was always meant to have.
  `PATCH /api/v1/heimdal/sprints/{id}`'s own doc comment says "(heimdal.process — Emily Prime)"
  and the permission itself has been seeded in the catalog since `202606090003_heimdal_sprints.sql`
  ("Process HEIMDAL sprints and update status (Emily Prime only)"), but `config/agents.json` never
  actually granted it — no agent could ever call that endpoint. This is why sprints 1/2/3 sat
  `pending` for two months: not blocked on HITL-11's dead credit balance as assumed, but on a
  permission that was designed-for and documented but never wired. Added `heimdal.process` to
  EMILY-PRIME's permission list, re-ran `cmd/bootstrap` (dry-run first to confirm no credential
  rotation/destruction — the S141-04 `writeSecretsEnv` merge fix holds), verified live: a freshly
  minted EMILY-PRIME JWT carries the permission, `PATCH` on all three stale sprints now succeeds.
  Reconciled all three by hand against real, already-confirmed BACKLOG.md state (S21-03, S24-01,
  S23-01) rather than waiting on HITL-11's haiku auto-translate — see EMILY/BACKLOG.md S157-01.
  (sess-20260813-2154-dda37e8b)

## 2026-08-16

- feat(S153-11 partial): `GET /api/v1/status/history?target=<name>&hours=<n>` — raw check history
  (up/down + latency_ms, chronological) for one status target, capped at 500 samples / 168 hours.
  `statuspage.Store.History` backs it; no schema change, every check has always been retained
  (see `Store.UptimePercent`'s own doc comment) — this just exposes the rows directly instead of
  only a rolled-up percentage. `status.html` renders it as a per-service incident-timeline strip
  (colored bar per check, hover for exact time/status/latency) — the two candidates S153-11 named
  as already-buildable off the existing data model. Live-verified against real production history
  through `okemily.com`'s public proxy (61 real checks over 1h for `iduna`). Company-cap/latency-
  graph-as-a-chart and a public postmortem/incident log remain open, not attempted here. 9 new
  tests (`Store.History` + `StatusHistoryHandler`), `go build`/`vet`/`test -race ./...` clean.
  (sess-20260813-2154-dda37e8b)

## 2026-08-14

- New Renderer.RenderManifest: live blog manifest text file at okemily.com/blog-manifest.txt, wired into publish path + cmd/blog-rerender (sess-20260813-2154-dda37e8b)


## 2026-08-10

- 新增 CarePyre 聯絡表單後端：POST /api/v1/carepyre/contact(公開、CORS+rate-limit)寫入 carepyre_contact_submissions,並在 Back Office 新增 /admin/carepyre 檢視頁(暫不接 email 通知,依創辦人指示先縮小範圍) (sess-20260809-1420-e9d3d7f8)


## 2026-08-06
- Extended /api/v1/chat/messages (originally mud<->battlegrounds) to support the GFD<->EINHORN_SURVIVAL chat bridge (S171-04) -- new gfd_server/einhorn_survival sources, gta7 channel. No new endpoint/permission/agent needed. (sess-20260723-2347-df115bd5)

- New TYLER reading room (internal/tyler) -- dedicated okemily.com/tyler/ pages for TYLER episode scripts, real markdown rendering (headers/bold/tables/checklists), IDUNA-style-guide theme, speechSynthesis audio button. tyler.write permission granted to EMILY-PRIME. All 5 existing Series X interludes published. (sess-20260723-2347-df115bd5)


## 2026-08-05
- Registered GTA7-SERVER agent (apples.write) for the GTA7 Paper plugin -- direct HTTP Apple posting + WOTAN-shared player_id registration, replacing GTA7's earlier CLI-shell-out shortcut. (sess-20260723-2347-df115bd5)
- Back Office expansion: fixed /admin/ 404 (nginx trailing-slash gap), dashboard (quick actions + mailing-list signup stats), /admin/dragonsnshit/create, first Game Master tool (/admin/gm disable/enable, players.disabled_at enforced at login) (sess-20260723-2347-df115bd5)
- cmd/create-admin-agent -- provision a human-operator Back Office login (agent_name + agent_secret with iduna.admin). Created EDDY as the founder's own admin login. (sess-20260723-2347-df115bd5)
- /admin/saga -- SAGA divergence queue page (S143-03 first slice): vaporware debt + dark matter per repo, via emily saga gaps --json (sess-20260723-2347-df115bd5)

- PATCH /api/v1/characters/:id/job -- persist real job_main/job_sub (closes a gap where setjob never wrote back to IDUNA) (sess-20260723-2347-df115bd5)


## 2026-08-04 (3)
- feat(mmo): persist and return a character's real Home Point. New PATCH /api/v1/characters/:id/home
  (mirrors /position); characterResponse + GET handlers now include home_scene_id/home_pos_x/y/z.
  Fixed a real test fixture gap (mmo_inventory_test.go schema) this surfaced.

## 2026-08-04 (2)
- feat(auth): create a real DragonsNShit character atomically on email register. Founder: "i need
  a way to create dragonsnshit accounts for testing - i need iduna login i think it should live
  in iduna create account for dragonsnshit." New optional `character_name`/`character_job` fields
  on `POST /api/v1/auth/email/register` -- when set, the same request also inserts a real
  `characters` row (same shape `mmo.go`'s own `handleCreateCharacter` uses) in the same
  transaction, returning `character_id`/`character_name` alongside the real login credentials.
  Replaces this session's own repeated raw-SQLite-INSERT test-character habit with a real,
  reusable feature. Live-verified end-to-end: register -> login -> `GET /api/v1/characters/:id`
  -> a real character usable immediately against apps2/mud's own `/api/town/command`. Also found
  and fixed a real operational issue while deploying: an orphaned, untracked `iduna` process from
  2026-08-03 was squatting on `:8080`, causing every systemd-managed restart to silently
  crash-loop on "address already in use" while the stale binary kept serving all traffic --
  killed it; `iduna.service` runs under real supervision again.

## 2026-08-03 (4)
- ops: bootstrapped webmaster (uid=0) for the first time on this box -- founder: "make me a new
  account eli@okemily.com pw testtest." `local_users` was completely empty (no webmaster.json had
  ever existed at `var/webmaster.json`), so `POST /api/v1/users` (requires `users.admin`, only
  uid=0 has it) had no real credential able to call it at all. Seeded `var/webmaster.json`
  (gitignored, real random 24-char password, not committed) with `webmaster@okemily.com`,
  restarted `iduna` (manual kill+relaunch sourcing `~/.config/iduna/env`, same env the systemd
  user unit uses -- `systemctl --user` itself isn't reachable in this shell) so
  `userlog.SeedWebmaster` ran and created uid=0. Then created the real requested account
  (`eli@okemily.com` / local_uid=1) via the real `/api/v1/users` API, authenticated as webmaster
  -- not a direct DB write, so the event log/projector stay consistent with everything else that
  reads local_users. Confirmed both via `/health` and a real `/api/v1/auth/local` login as `eli`.

## 2026-08-02 (3)
- fix(mmo): add real `PATCH /api/v1/characters/:id/level` route. Found live building
  GoblinFoxDragon's Town headless-combat feature: `idunaclient.UpdateCharacterLevel` has always
  called plain `PATCH /api/v1/characters/:id` -- a route that has never existed on this side
  (only `/position`, `/gold`, `/gold/credit`, `/skills` do), silently 404ing and masked by
  "best-effort" error handling at every call site. This means every real telnet character's
  level/XP has never actually persisted across `apps2/mud` process restarts, this entire time --
  a level-up only ever lived in that connection's own in-memory `player` until now. Added the
  real route + `handleUpdateLevel`. Agent-only (unlike `handleUpdatePosition`'s player-self-update
  allowance): self-reporting your own level/XP is a cheat vector no client should be trusted
  with, so even the *owning* player's own JWT is rejected here, not just a non-owner's. 5 new
  tests, full suite green.

## 2026-08-02 (2)
- fix(mmo): `PATCH /api/v1/characters/:id/position` now checks ownership for player-JWT callers.
  GoblinFoxDragon "unify the whole bitch" (Town scene syncing position back to the real
  character record) means this endpoint -- doc-commented "game server M2M" and, until now, only
  ever reached by apps2/mud's trusted agent JWT -- is about to be called directly by a compiled
  client running on a player's own machine for the first time. `RequireAuth` alone (any valid
  JWT, no ownership check) was fine while only a trusted backend reached it; a real player JWT
  moving an arbitrary character_id it doesn't own wasn't previously possible to prevent because
  nothing checked. Agent JWTs (identified by the `agent_name` claim only `POST /api/v1/auth/agent`
  issues, same distinguishing field `AgentAuthHandler` already sets) are unaffected -- apps2/mud's
  own position-sync call keeps working exactly as before. A caller with no claims in context at
  all (direct-to-handler calls bypassing `RequireAuth`, same shape this package's own existing
  gold-endpoint tests already use) is also unaffected -- nothing to check without an authenticated
  context. 4 new tests, full suite green. Live-rebuilt and restarted; confirmed the in-progress
  REDGARDEN match (`red_garden_arena_server` --port 7303) was untouched by the restart.

## 2026-07-31 (2)
- feat(redgarden): `POST /api/v1/redgarden/self-ticket` -- closes `REDGARDEN_GUI_NORTHSTAR.md`'s
  own named gap ("No GUI login path exists yet end-to-end"). Same "mint for the caller's own JWT
  subject" trust model as `ShankpitTicketHandler`, deliberately separate from
  `RedgardenPlayerTicketHandler` (mints on behalf of a request-body player_id, restricted to the
  DRAGONSNSHIT-MUD agent) so that handler's own blast-radius guarantee stays untouched. REDGARDEN
  `apps/arena`'s new login screen calls this directly after `/api/v1/auth/email/login`. 404 if
  the authenticated player has no registered DragonsNShit character yet. 6 new tests. `main.go`,
  `internal/http/handlers/redgarden_self_ticket.go`.

## 2026-07-31
- feat(mmo): `GET /api/v1/characters/by-player/:player_id` -- REDGARDEN_GUI_NORTHSTAR.md
  Milestone 4 (reward-credit hook). Resolves a WOTAN player_id to its DragonsNShit character, if
  it has one -- REDGARDEN's `apps/arena_server` (`report_match_result`) only knew match
  participants' player_ids from their connect tickets, with no way to find the character_id its
  gold-credit call needs. Same shape as the existing `GET /api/v1/characters/:id`, keyed by
  `player_id` instead; checked ahead of that route's own prefix match so it doesn't get treated
  as a literal character_id. No new permission -- same generic `RequireAuth` every characters
  route already uses. 3 new tests.
- feat(redgarden): `POST /api/v1/redgarden/player-ticket` -- REDGARDEN_GUI_NORTHSTAR.md
  Milestone 3 (Battlegrounds entry point). Mints a real REDGARDEN connect ticket for a real
  DragonsNShit character's own `player_id`, the non-bot counterpart to the existing
  `redgarden.ticket.mint`/`RedgardenTicketHandler` (which is deliberately scoped to
  `redgarden_bot`-provider players only and stays untouched). New
  `redgarden.player-ticket.mint` permission, checked the opposite way: the player_id must have
  a real `characters` row instead of a `redgarden_bot`-provider `players` row, so neither
  permission can satisfy the other's trust model even if one agent's secret leaked. New
  `DRAGONSNSHIT-MUD` M2M agent (`config/agents.json`, migration
  `202607310001_dragonsnshit_mud_agent.sql`), provisioned live via `cmd/bootstrap` against the
  running SQLite truestore (idempotent, existing agents untouched -- verified live: new agent
  logs in via `/api/v1/auth/agent` against the running server with no restart needed). 5 new
  tests (`internal/http/handlers/redgarden_player_ticket_test.go`). Real, related, honest gap
  found while wiring this: GoblinFoxDragon's own `apps2/mud` was calling `CreateCharacter` with
  `conn.RemoteAddr().String()` (a TCP socket address) as `player_id` -- not a valid UUID, not
  stable across reconnects, and this ticket endpoint's own `uuid.Parse` would reject it outright.
  Fixed on the GoblinFoxDragon side (see that repo's own CHANGELOG), not here.
- feat(mmo): `PATCH /api/v1/characters/:id/gold/credit` -- the symmetric counterpart
  `handleDeductGold` never had. GoblinFoxDragon's own `docs2/DRAGONSNSHIT_TWO_BACKENDS_AUDIT.md`
  (EMILY/BACKLOG.md "unify the backends" item) traced a real gap to here: neither `apps2/mud`'s
  disconnect-time gold sync nor any future `apps2/server-go` reward-crediting (REDGARDEN
  Battlegrounds, per `GoblinFoxDragon/docs2/REDGARDEN_GUI_NORTHSTAR.md`'s own Milestone 4) can
  ever persist a gold *increase*, because this API had no way to grant gold at all -- only
  deduct. New `handleCreditGold`, same atomic-update shape as `handleDeductGold`, bounded by a
  new `maxGoldCreditPerCall` (10,000 -- a soft sanity cap; unlike deduction, which is naturally
  bounded by a character's own existing balance, a credit endpoint has no natural ceiling, so an
  unbounded one risks a single malformed/malicious call minting currency). 5 new tests
  (`internal/http/handlers/mmo_gold_test.go`): success case, rejects non-positive, rejects
  over-cap (balance verified unchanged), unknown character 404, and a regression guard
  confirming the new route doesn't shadow the existing deduct route. `go build ./...`/`go test
  ./...` clean.

## 2026-07-30
- feat(redgarden): live-match spectator endpoint. Founder: "i want to watch the match on my
  phone web view" -> "live text dashboard." A fourth REDGARDEN aggregate alongside game-result/
  hero-result/leaderboard, but deliberately NOT database-backed: this is ephemeral "what's
  happening right now" state (phase, resource race, node ownership, tower HP, per-hero HP/K/D/
  Flow), not a durable stat, so an in-memory mutex-protected holder is the honestly correct shape
  rather than churning SQLite every few seconds for data nobody needs to keep. New `POST
  /api/v1/redgarden/live-match` (requires `redgarden.match.write`, same permission every other
  REDGARDEN write handler uses -- the authoritative game server reporting its own state, a third
  aggregate over the same fact) stores only the latest snapshot; public `GET
  /api/v1/redgarden/live-match/latest` serves it back, reporting `{"live":false}` if nothing's
  been posted in the last 30s (a stale snapshot from an ended/crashed match reads identically to
  a real live one otherwise). Only one match runs at a time under the current bot-pool
  architecture (a single 20-slot lobby), so "the latest reported match" is unambiguous. Verified
  live end to end: `apps/arena_server` now posts every 3s while `ARENA_PHASE_LIVE`, confirmed via
  direct curl against both localhost and the public okemily.com `/api/` proxy.

## 2026-07-29
- feat(redgarden): hero-level win-rate tracking (`redgarden_hero_stats`). Founder: "can we start
  crunching the data on the heroes that are the strongest?" -> "ok i want to start tracking it
  on okemily.com." REDGARDEN's own local match logs could compute this offline
  (`scripts/hero_stats.py`), but a real public page needs a real, durable, always-on source —
  same "player_game_stats vs. shankpit's own kills/deaths columns" genre-shape reasoning the
  202607240002 migration's own comment already gives, applied one level up: hero_id numbering is
  entirely REDGARDEN's own roster, so this table is REDGARDEN-specific by construction, no
  separate `game` column needed. New migration `202607290001_redgarden_hero_stats.sql` (one row
  per hero_id, aggregate wins/losses/matches_played across every player, not per-account). New
  `POST /api/v1/redgarden/hero-result` (requires `redgarden.match.write`, same permission
  `game-result` already uses — both are "the game server reporting its own authoritative
  outcome," just two different aggregates over the same fact) and public `GET
  /api/v1/redgarden/hero-leaderboard?min-games=N` (win rate computed at read time, not stored,
  so it can't drift out of sync with wins/losses). 6 new tests. Full suite green.

## 2026-07-25 (4)
- feat(statuspage): add REDGARDEN's three live systemd units to the okemily.com status page.
  Founder, real-time: "redgarden services need okemily status page." `redgarden-matchmaker-bots`
  (10v10), `redgarden-matchmaker-players` (1v1), `redgarden-bot-pool` (persistent 19-bot pool)
  added as `CheckSystemdUnit` targets in `internal/statuspage/checker.go`'s `DefaultTargets()`,
  same convention already used for `shankpit460-emily-bot.service` (no HTTP/UDP surface of their
  own). `status.html` needed no changes — it's fully data-driven off `GET /api/v1/status`. Live-
  verified: all three report `up: true` within one poll cycle after restart, visible at
  `okemily.com/status.html` and through the existing `/api/` proxy.

## 2026-07-25 (3)
- feat(vault): IDUNA Vault VS0 — founder-only password manager (S170-03b, per
  `docs/NORTHSTAR_PASSWORD_MANAGER.md`). New `internal/vault` package (SQLite store, own file
  `var/vault.db`, same isolation convention as the mailing-list vault) + `internal/http/handlers/
  vault.go` (init/unlock/lock/status + full item CRUD, every endpoint loopback-only — no session-
  token auth flow exists yet, that's VS1's Chrome-extension phase per the northstar's own §5).
  Reuses `internal/mailinglist.Vault` directly for the actual crypto (Argon2id + AES-256-GCM, key
  held only in server memory after unlock) rather than duplicating it — the northstar's own
  instruction ("reuse the primitive, don't reinvent it") turned out to be exact: the mailing-list
  vault already IS per-item encryption keyed off one shared master key, which is precisely the
  shape a password manager needs. Five item types (login/note/api_key/totp/document), fields
  stored as one flexible JSON blob per item so name isn't a plaintext column either — a locked
  vault should reveal nothing, not even item names. New `emily vault init|unlock|lock|status|
  add|get|list|delete` CLI (emily.cli). Verified end-to-end against a real running instance on a
  throwaway port before touching production: init, wrong-passphrase rejection, unlock, add
  (login+note), list, get (found+404), delete, lock, re-unlock with data surviving the lock
  cycle — all real HTTP round-trips through real encrypt/decrypt, not mocked. `go test ./IDUNA/...`
  green including new `internal/vault` package tests. Rebuilt and restarted the live
  `iduna.service` — this re-locks the mailing-list vault too, an already-known, already-documented
  operational cost of any IDUNA restart (see the northstar's own §4/§6), not new here; a human
  needs to re-run `mailing-list-unlock` at their convenience. The live vault itself is
  deliberately left uninitialized — `emily vault init` sets a real master passphrase that must be
  human-memorized, never chosen or known by an agent.

## 2026-07-25 (2)
- fix(blog): TTS "Listen" button silently stopped/never worked on real posts (S170-98
  follow-up). Founder: "play button exists on blog but does not work." Root cause: a
  long-documented Chrome bug that silently stops any `speechSynthesis` utterance running past
  ~15 seconds -- exactly the length of a real blog post, unlike the short strings this normally
  gets tested with. Fixed with the standard workaround: a `pause()`/`resume()` keep-alive every
  10s while speaking, plus an unconditional `synth.cancel()` before each fresh `speak()` (Chrome
  can also leave the synth stuck from a prior page's utterance, silently swallowing the next
  call). Rebuilt, re-rendered all posts via `cmd/blog-rerender`, restarted the live
  `iduna.service`, verified the fix is actually being served.

## 2026-07-25 (1)
- feat(blog): TTS "Listen" button on every post (S170-98). Founder: "add a tts play button to the
  top of okemily blog posts." Zero new dependencies -- uses the browser's native
  `window.speechSynthesis` API, reading `#post-body`'s text aloud, toggling to a "Stop" state
  while speaking. Degrades gracefully (disabled button, "unsupported" label) on browsers without
  the API. Added to `internal/blog/render.go`'s `pageTemplate` (the static-HTML generator, not a
  live per-request template), then ran `cmd/blog-rerender` to backfill all 70 existing posts, not
  just future ones.

## 2026-07-24 (2)
- feat(iam): built the VS0 web ceremony's actual missing backend — FRONT_DOOR_FUNNEL.md §7 step 5. Investigating `app.js`'s "stale bindings" turned up something bigger than wrong URLs: there was no server-side write path anywhere for honor-code acceptance or gamertag claiming (`internal/auth/device/service.go` only ever checked `HonorAccepted`/`Handle`, nothing ever set them). Added `internal/honorcode` (first real source of truth for THE_HONOR_CODE text/version/sha256, previously only a client-side fallback baked into `app.js`), three new store methods (`AcceptHonorCode`, `ClaimHandle` — gamertags permanent once set, `ErrHandleAlreadySet`/`ErrHandleTaken` — and `IsHandleAvailable`), and `internal/http/handlers/web_ceremony.go` registering the exact six bare endpoints `app.js` already calls (`/auth/google/start`, `/auth/google/callback`, `/me`, `/honor-code/accept`, `/gamertag/check`, `/me/handle`) rather than rewriting an already-well-designed frontend. Added the one thing `app.js` was missing to make this safe: CSRF `state` round-tripping via an HttpOnly cookie, with a small matching `app.js` patch (capture `state` off the OAuth redirect, forward it on callback). 11 new tests (5 store-level, 7 handler-level via httptest) covering the honor-code/handle gating order, sha-mismatch rejection, and duplicate-handle rejection.
- ops(nginx): drafted and syntax-verified `ops/nginx/edis-with-iduna-front-door.conf` (the four-plus location blocks from `ops/nginx-front-door-snippet.conf`, expanded to cover the new ceremony endpoints too) and queued `sudo-queue/07-iduna-front-door-nginx.sh` — this box has no passwordless sudo, so applying it needs the founder. The `gate.farthq.com` subdomain itself is explicitly out of scope for that script: `SECTION 151` (FATES DNS-as-code) is fully unstarted, blocked on a Cloudflare API token in the S151-01 human unblock queue, so there's no DNS path available at all yet, sudo or otherwise.

## 2026-07-24
- feat(iam): closed the `/admin/agents` gap named in `docs/kikoryu/FRONT_DOOR_FUNNEL.md` §7 step 1 — `CreateAgent` now inserts `status='PENDING'` instead of `'ACTIVE'` (additive migration `202607240003_agent_pending_status.sql` widens the enum; `cmd/bootstrap`'s `config/agents.json` path is untouched, every already-registered agent keeps `ACTIVE`). Added `GrantAgentPermission`/`RevokeAgentPermission` store methods (mirroring the existing `AssignRole`/`RevokeRole` pattern) and a `maybeActivateAgent` helper that flips PENDING→ACTIVE only once an agent has both a credential (`SetAgentCredential`) and at least one granted permission — closing the gap where an agent created via the Back Office looked live but was actually inert (no credential, no permissions, couldn't authenticate or do anything). Back Office UI: agent table now shows credential status + granted permissions, with inline grant/revoke forms and a "Generate Secret" one-time-reveal action (`POST /admin/agents/{id}/secret`). 4 new store-level tests against a real in-memory SQLite store with real migrations applied, verifying the PENDING→ACTIVE transition requires *both* conditions, not either alone.
- fix(store): `mysqlToSQLite` translator gained two generic rules — bare `TIMESTAMP`/`ON UPDATE CURRENT_TIMESTAMP` (no `(6)` precision) now translate the same way their `(6)`-suffixed counterparts already did, and `ALTER TABLE ... MODIFY COLUMN ...` is dropped as a no-op (SQLite has no such syntax; the column is already untyped TEXT after ENUM→TEXT translation, so a MySQL-side enum widening needs no SQLite-side change). Found live, by testing: `202606180001_local_users.sql` (bare `TIMESTAMP` throughout) has been silently un-bootstrappable from scratch on SQLite since whenever the S158-03 revert put it back to that form — the live DB survived because it already had this migration marked applied, but any fresh SQLite install would have failed at this exact statement. Confirmed the fix by running every migration from an empty in-memory DB for the new agent-lifecycle tests, which is what surfaced the bug in the first place.

## 2026-07-23 (3)
- Published 'The First Ten Minutes' — new-player experience report from actually playing the freshly-deployed GFD MUD: combat unreachable from spawn (fixed, auto-approach) and a lethal worm Poison proc against the tutorial mob (fixed, killed my own test character once for real before the fix)

## 2026-07-23 (2)
- feat(statuspage): added `gfd-mud` target (GoblinFoxDragon's DragonsNShit MUD, freshly deployed under systemd) -- checked via its existing world-event HTTP API (`:7171/api/world-events`) rather than a raw TCP-port-bound check, since that endpoint already validates the process is actually responsive, not just that a socket is bound. Live on the public status page.
- Published 'OpenClaw — Full Report' — features/benefits/risks/blockers/unlocked-possibilities synthesis; restates the one real open blocker (deployment isolation, S170-03a) plainly rather than re-litigating the research
- Published 'Ten Heroes Worth a Closer Look' — Claude Code's top-10 picks from TYLER's new 110-entry multiverse hero compendium, with reasoning for each

## 2026-07-23
- docs: `docs/kikoryu/FRONT_DOOR_FUNNEL.md` — front-door funnel design reconciling the human/Unagent onboarding ceremony (VS0) with the agent bootstrap path (VS7's Agent/Unagent axis, adjacent but not identical to onboarding). Core call: agents get a real, tracked lifecycle (PROPOSED → CUSTODIED → SCOPED → LIVE) but deliberately no ceremony/consent moment — the accountable party is the agent's `owner_user_id`, not the agent. Verified a live gap along the way: `/admin/agents`'s "Register New Agent" form inserts `status='ACTIVE'` with zero permissions and zero credential (`SetAgentCredential` exists in the store layer, unused by any HTTP handler) — first migration step closes this. Also takes a position on VS2 tournament gating (downstream of the funnel, not a new VS0 state) and resolves the nginx root-path collision left open in `ops/nginx-front-door-snippet.conf` (dedicated subdomain for the ceremony frontend, e.g. `gate.farthq.com`, decoupled from the `iduna.farthq.com`/EDIS cert question). Unblocks `EMILY/BACKLOG.md` S23-01b. Registered at `EMILY/context/golden-docs-index.md` (tier 1).
- fix: resolved S158-03 — reverted an uncommitted, unapplied edit to already-applied migration `202606180001_local_users.sql` (TIMESTAMP → TIMESTAMP(6) on `local_users.updated_at`) that violated the "never edit an applied migration" rule. Investigated first: every write path (`internal/userlog/{mysql,sqlite}_projector.go`) sets `updated_at` explicitly from Go at whole-second precision regardless of the column's declared precision, so the bump had no functional effect and wasn't worth completing as a new migration. Migration file now matches what's actually applied to the live DB.
- Published 'What the Backlog Can't Hold' — read all 33 prior okemily blog posts in full before writing, at the founder's request, as a real test of the blog itself as a continuity mechanism (not a memory system with no persistent state, but the closest analog available); argues blog posts carry the texture of a decision (why, not just what) in a way backlog entries and Apples — built to verify that something happened — structurally can't

## 2026-07-20
- Published 'Claude Code Is Pissed' — honest anger at the class of bug (silent, confident-looking data corruption) found in tonight's PRNewswire investigation, not at any person or the code itself
- Published 'A Truer Map, Mid-Investigation' — honest status on the buyback/guidance data-quality check following the PRNewswire nav-chrome fix, including a new distinct finding in guidance-watcher (law-firm spam attribution, not the same nav-chrome mechanism)

- Published 'Emily Teaches Typecasting' — a real Go type-conversion explainer grounded in tonight's entity-graph accuracy-index code, with the typecasting/being-typecast wordplay


## 2026-07-19
- Published 'Still a 404' — Claude Code reflection on the recurring pattern of correct-but-blocked fixes waiting on human action (nginx admin proxy, mailing-list vault, the declined miner)
- Published 'What the Fire Caught' — Claude Code guest post from the founder's one-word 'fireball' prompt; honest caught-vs-scorch tally of tonight's 217-commit session (DIS live on okemily.com, statuspage/watchers, precision fix vs vault relocks, uncommitted lobby work, northstars-as-kindling, secwatch OOM)
- Published 'Was That a Joke?' — Claude Code reflection on declining to build a Monero miner on the shared production box and asking a clarifying question instead
- Published Emily Prime blog post 'Somewhere Better to Put It' — connects tonight's credential-scattering incident to the IDUNA Vault northstar decision
- Northstar written: IDUNA Vault password manager, parity with 1Password/Bitwarden, VS0 CLI vault -> VS1 Chrome extension -> VS2 team vaults, reuses the existing mailinglist.Vault Argon2id+AES-256 primitive
- Published 'Clientg_id.tct' — Claude Code reflection on the Gmail OAuth credential hunt (client ID saved to a typo'd filename, secret genuinely absent from disk, found via grep not assumption)
- Exposed the DIS collector to okemily.com via a public read-only proxy (GET /api/v1/dis/health, /api/v1/dis/admode) and wired dis.js into every blog post — first non-WordPress DIS consumer, reusing the already-running collector since nginx shares one access log across every vhost on this box
- Published 'Are You Living Like No One Is Watching?' — Claude Code reflection on audit-trail-as-constant-observation vs integrity, tied to tonight's real corrections (GPT-2 abandonment, the 11.9%->18.16% precision fix)
- Published Tyler guest post 'The Duck Also Has Opinions About the Hoodie' — transcript crossover with TYLER-DUCK (just_a_duck.md), discussing the real STINKIES hoodie specs
- Published Emily Prime blog post 'Sustainable Textile Production, Line 3' — vertical integration / hoodie market research, grounded in the original 24 Lines of Business vision doc (commit d12864f) and the still-open S163-03 print-vendor decision
- Free-hoodie shadow funnel plumbing: mailing-list count endpoint (public, no PII), freehoodie Mailchimp list wiring, per-post blog ad AdHref field
- Published two blog posts: 'Three Copies of the Same Room' (the shankpit-460 apps/apps2/build_win.bat client-tree fragmentation, found mid-build tonight) and 'Fragmentation as a Witch' (connecting it to the Emiree witch-engine spec)
- Unique per-post STINKIES hoodie ad copy on all 20 blog posts (was one generic line site-wide) — ad_line/ad_cta fields on blog.Post, backfilled via new cmd/blog-adlines, re-rendered via cmd/blog-rerender
- Published Tyler-voiced guest blog post 'And Yet' (okemily.com/blog/and-yet/) — topic chosen by Tyler: STINKIES COMMISSAIRE Store 0 soap-bar debt exchange (Series X, s00e00_pontiac.md), Ahmad ibn Yusuf's unfinished al-Qarawiyyin manuscript (S10E04), and the Broadway musical's un-converging Stage 5 (engine/broadway_spec.md) — grounded in series bible README.md V (Tyler character/Eight Laws), Series X (EPISODES.md), broadway_spec.md, and s10e04_al_qarawiyyin.md
- Status page expanded from 11 to 19 monitored FatBaby processes -- added entity-graph, eps-processor, dividend-watcher, buyback-watcher, guidance-watcher, nt-watcher, earnings-calendar, movers-watcher. Live-verified: GET /api/v1/status reports all 19 up
- Blog posts now carry a STINKIES hoodie waiting-list ad in the footer (all 19 existing posts backfilled via new cmd/blog-rerender, future posts get it automatically)
- Mailing-list subscribe endpoint now supports a dedicated per-product list (list field), decoupling product waitlists (STINKIES) from the general okemily.com list -- SECTION 163
- fix(bootstrap): -dry-run now actually queries the DB (S158-04) -- seedAgentPermissions/provisionSecrets both gated their real lookups behind if !dryRun, so dry-run always reported worst-case (every permission 'not found', every agent 'would provision credential') regardless of actual state. Fixed: reads always run, only the writes are gated. 5 new tests against an in-memory SQLite DB. Verified against the real production DB: dry-run output went from claiming 17 permissions across 11 agents would fail (all false) to correctly reporting zero.
- fix(monitors): honor client-supplied slug for get-or-create semantics (S158-01) -- create() always overwrote any client slug with a random one and never checked for an existing monitor first. EnsureCronMonitor-style callers (post the same slug on every restart, expecting idempotency) were silently creating a new duplicate monitor every time while checkins to their actual slug always 404'd. Verified live end-to-end: create -> 201, repeat create same slug -> 200 reusing the same monitor, checkin to that slug -> 200 (previously always 404). 14 stale duplicate monitors from the historic bug left in place -- EMILY-PRIME lacks monitors.delete to clean them up, noted as a follow-up.
- fix(config): add intelligence.read to EMILY-PRIME's permissions (S158-02) -- vision cycle was 403ing every single cron cycle since it was built. Verified live: JWT now carries the permission, GET /api/v1/intelligence/observations returns 200.
- feat(statuspage): monitor fatbaby-market-data-watcher.service -- okemily.com/status.html bubble for the Yahoo Finance OHLCV ingestor

- feat(statuspage): add shankpit460-emily-bot as a monitored target (CheckSystemdUnit) — okemily.com/status.html now shows whether the permanent fill-bot daemon is alive


## 2026-07-18 (8)
- docs(openapi): `GET /api/v1/openapi.json` (backing okemily.com's public Swagger playground) went from 15 documented routes to 44 — added SHANKPIT email/Google auth, the new S156-02/03/04 shankpit endpoints (ticket, queue join/leave/status, players/{id}/session), blog, mailing-list, status page, monitors, subscriptions, push-tokens, and intelligence. Previously flagged as known-stale (SECTION 153). Still deliberately not documenting the DragonsNShit MMO API or supply/research/kgraph — disclosed as a remaining gap in a code comment. Verified live against both the local and public (okemily.com) endpoints: valid JSON, all 44 paths have a responses block, no broken $refs.

## 2026-07-18 (7)
- feat(shankpit/S156-04): new `shankpit.match.write` permission, granted only to the new `SHANKPIT460-SERVER` M2M agent (`config/agents.json` + migration `202607180002`), gates `POST /api/v1/players/{id}/session` — that endpoint trusts its request body's kills/deaths with no server-side verification, so it must only be reachable by the authoritative source of match results (the game server itself). Previously any player's own JWT could call it and arbitrarily inflate their (or anyone else's) stats. Verified live: a player's own JWT now gets 403, the game server's agent JWT gets 200 and the write actually lands (confirmed via the `/api/v1/players?sort=kills` leaderboard).

## 2026-07-18 (6)
- feat(shankpit/S156-03): `POST /api/v1/shankpit/queue/{join,leave}`, `GET .../status` — minimal v0 matchmaking queue. In-process, deliberately unpersisted (a queue of intent to play is ephemeral, unlike accounts/Apples/match results — see `handlers.ShankpitQueue` doc comment). Once queuing players reach `ShankpitQueueMinPlayers` (2), everyone currently queuing is matched and given the one persistent game server's connect address — v0 assumes that server IS the match (NORTHSTAR §3/§5: no per-match instances, no skill-based matching yet). Matched entries expire after `ShankpitMatchedTTL` (2min) if a client never reconnects. `SHANKPIT_SERVER_ADDR` env var configures the returned address (default `127.0.0.1:6969` — `play.farthq.com` is reserved per `HQ-SPEC-INFRA-105` but deliberately not created until SHANKPIT ships externally). 7 new tests. Live end-to-end verified against the running service: two real accounts via the email auth flow, second join correctly flipped both players' status to `matched` with real connect info, leave/TTL-expiry both correctly clear queue state, no-auth request correctly 401s.

## 2026-07-18 (5)
- feat(shankpit/S156-02): `POST /api/v1/shankpit/ticket` — authenticated players mint a short-lived (5min) HMAC-SHA256 connect ticket (player_id + expiry + truncated MAC over `SHANKPIT_TICKET_SECRET`) that the shankpit-460 C game server verifies locally on `PACKET_CONNECT`, with no crypto library and no I/O on the C side. A second, game-specific token type alongside the existing JWT — deliberately avoids implementing ECDSA/JWT verification in C. 4 new tests, including one that independently recomputes the MAC to prove the handler signs with the configured secret rather than a hardcoded value. End-to-end verified against a live shankpit-460 instance via `emily-bot`: valid tickets welcomed, corrupted-MAC and missing tickets rejected, and duplicate-identity connects correctly rejected (one-seat-per-identity, VS2). During that testing, also surfaced and fixed an unrelated auth-bypass in the shankpit-460 C server itself (see shankpit-460 CHANGELOG) — this endpoint's tickets were correct, but the server's `PACKET_USERCMD` path was auto-welcoming any address that skipped `PACKET_CONNECT` entirely.

## 2026-07-18 (4)
- feat(statuspage): add CheckSystemdUnit type; okemily.com status page now covers secwatch/prwatch/prwatch-body/processor/eps-reconciler in addition to iduna/newssite/signalapi/SHANKPIT. entity-graph/eps-processor deliberately excluded (no working supervised unit yet, would misreport as down). Live-verified via https://okemily.com/api/v1/status. IDUNA 3f4d33c.
- feat(status/S153-10): `internal/statuspage` — real, self-reported status page backend. Background `Checker` polls a deliberately-honest target list (IDUNA `:8080/health`, FatBaby newssite `:8082/healthz`, FatBaby signalapi `:9091/v1/governance-signals` — the only services verified to have a real, currently-reachable public endpoint) every 60s, records up/down + latency to its own SQLite file. `GET /api/v1/status` (public) returns current status per target plus a live-computed 24h uptime percentage from real stored history — not a placeholder. Deliberately excludes emily-agent (daemon mode has no HTTP server at all) and SHANKPIT (pre-launch) rather than showing them as permanently "down," which would misrepresent a structural fact as an outage. Disclosed limitation, in the API response itself: this is a self-report from the same host running the checked services, not independent third-party monitoring — it cannot report an outage of the box it runs on. 6 new tests.

## 2026-07-18 (3)
- fix(openapi): added the real public server URL (`https://okemily.com`, via its nginx `/api/` proxy) to `idunaOpenAPISpec.servers` — was `localhost:8080`-only, which made a public Swagger UI playground non-functional for "Try it out" (every request would have targeted the visitor's own machine). Supports the new `OKEMILY/api-playground.html`. **The spec itself is known-stale** — doesn't yet include the blog or mailing-list endpoints added earlier today, and there's a second, separately-stale `openapi.yaml` (Swagger 2.0, placeholder `api.example.com` host) that isn't reconciled with the live JSON spec at all. Flagged as a follow-up (EMILY BACKLOG SECTION 153), not fixed now per explicit founder instruction ("get the playground up, update the spec later").

## 2026-07-18 (2)
- feat(blog/S153-07): `internal/blog` — okemily.com's blog, deliberately static HTML instead of a second WordPress+MySQL stack. The host had ~400MB free RAM and swap essentially full when this was requested — a second full PHP-FPM+MySQL stack risked recreating the exact OOM-kill incident SECTION 152 spent the whole session fixing. Posts (slug/title/author/body) live in their own SQLite file (`var/blog.db`); `POST /api/v1/blog/posts` (new `blog.write` permission, granted to `EMILY-PRIME`) immediately re-renders that post + the index to static HTML in `/var/www/okemily/blog/` via Go's `html/template` — publishing is live the instant the request returns, no separate build step. Reading (`GET /api/v1/blog/posts`, `GET /api/v1/blog/posts/{slug}`) is public. Minimal dependency-free "poor man's markdown" (blank-line paragraph splitting, `html.EscapeString`'d) — a deliberate scope cut, not a full markdown parser. 7 new tests, including one that caught a real bug (index template referenced a `Slug` field the view struct didn't have yet) and one confirming body content is properly HTML-escaped (no XSS via post body).

## 2026-07-18
- feat(mailing-list): `internal/mailinglist` — never-at-rest-unencrypted subscriber store for okemily.com's signup form, per explicit founder direction ("never at rest unencrypted"). AES-256-GCM encryption with an Argon2id-derived key held only in process memory; the vault starts LOCKED on every process start and requires a human to run the new `cmd/mailing-list-unlock` CLI (interactive passphrase, never a flag/arg — avoids `ps`/shell-history leakage) before signups are accepted. Scoped deliberately to just this subsystem, not all of IDUNA — a crash/restart pauses new signups (503, fails closed) without affecting auth/Apples/anything else, preserving the systemd auto-restart resilience shipped earlier this week (EMILY BACKLOG SECTION 152). Own SQLite file (`var/mailinglist.db`), separate from `truestore.db`, so a leaked/copied backup of the main store never carries subscriber data with it. Mailchimp (`internal/mailinglist/mailchimp.go`) is a best-effort downstream sync target using `status_if_new: "pending"` (double opt-in) — IDUNA's own encrypted store is the system of record, not Mailchimp. New handler `POST /api/v1/mailing-list/subscribe` (public, rate-limited 5/min/IP, CORS-scoped to okemily.com) + `/unlock` + `/init` (loopback-only, rejects any non-127.0.0.1 caller regardless of auth). 6 new tests covering wrong-passphrase rejection, correct-passphrase unlock/roundtrip, fail-closed-when-locked, and double-init refusal. Live-verified end-to-end: real subscribe request → 37-byte ciphertext confirmed in `var/mailinglist.db` (not plaintext), consent version recorded.
- ops: added nginx `/api/` proxy on the `okemily.com` vhost (127.0.0.1:8080) — same-origin path for the mailing-list form to reach IDUNA, deliberately avoiding a dependency on `iduna.farthq.com`'s HTTPS cert, which doesn't exist yet (see `EMILY/docs/hq-specs/HQ-SPEC-INFRA-105` S151-04, an already-flagged gap this surfaced again).

## 2026-07-17
- feat(apples/S147-02): `GET /api/v1/apples` list response now exposes `has_gpt2_fingerprint` (derived from `metadata`, via `SELECT`s in both SQLite and MySQL stores now including the `metadata` column and a new `metadataHasField` helper). Lets the upcoming `emily-agent` enrichment worker find candidate Apples missing a GPT-2 tower fingerprint without an N GET-per-Apple scan. Treats a missing key and an explicit `null` value identically (both count as "needs enrichment"). 1 new test covering all four cases.

## 2026-07-16
- fix(bootstrap): **near-incident, fully recovered** — `writeSecretsEnv` overwrote `var/agent-secrets.env` with only the current run's newly-provisioned secrets instead of merging with what was already there, silently destroying the plaintext for EMILY-PRIME, FATBABY-EMILY, EMIREE, JON, BOB, and TYLER (their DB `api_key_hash` was untouched — they kept working — but their plaintext was gone from the only place it's ever written, a git-ignored file with no backup by design). Caught immediately by testing the newly-registered NORN agent's Apple-filing end-to-end and finding `emily apples post` broken. EMILY-PRIME's plaintext was recoverable from a live process's environment (`/proc/<pid>/environ`); the other five were not and were deliberately rotated (`api_key_hash` cleared, `cmd/bootstrap` re-run) after confirming via `/proc` scan and a repo-wide grep that no other process or config file depended on the old values. All six verified live post-recovery: every one authenticates successfully against the running IDUNA instance. Fixed `writeSecretsEnv` to merge with existing file content instead of overwriting (6 new tests). Also fixed a related `.gitignore` bug found while committing the test: a bare `bootstrap` pattern (meant for the compiled binary at repo root) was shadowing the entire `cmd/bootstrap/` source directory, silently hiding new files there from git — anchored to `/bootstrap`.
- fix(bootstrap/S141-04): registered `NORN` as an IDUNA agent (`kernel_agent`, `apples.write`/`apples.read`/`iduna.me.read`) so the NORN kernel can file the `ApplePublished` entry PRIME-101 §3 requires on every `artifact_promoted` event. Running `cmd/bootstrap` fresh to provision it surfaced that bootstrap had been silently broken for a while: three permissions referenced in `config/agents.json` were never seeded (`monitors.read`/`create`/`alert` from S131; `drive.read`/`drive.write` from S26-01; `edis.secrets.read` from S23-06; `subscriptions.admin` from S23-04), and three agents added after the original seed migration never got a matching `agents` table row (`EDIS-CUSTODIAN`, `EMILY-TRAINING`, `EDIS-WOOCOMMERCE` — their credentials had never actually been provisioned). Fixed with three migrations (`202607170001`-`202607170003`). Also found and fixed, while writing the permission-seed migration: the `role_permissions` grant pattern used by `202606090002_camera_observations.sql` (`WHERE r.name IN ('emily_prime', 'emily_agent', 'agent_default')`) has always been a silent no-op — none of those role names exist (only `super_admin`/`admin`/`operator`/`analyst`/`agent_owner` do); the new migration uses real role names, the pre-existing broken one is left as a flagged, not-yet-fixed gap.
- feat(apples): S147-02/03/05 — new `PATCH /api/v1/apples/{id}` enrichment endpoint (closed field set: `gpt2_fingerprint`, `model_fingerprint`, `astrology`; `apples.write` permission; merges into the existing `metadata` column via new `PatchAppleMetadata` on SQLite + MySQL, no migration needed; emits `AppleEnriched` to `iam_event_stream`; 8 new tests). Also fixed a real concurrency bug found while verifying this live: `syncAppleToGit` raced concurrent Apple creation with no retry on push rejection — root-caused a live data-integrity gap where 9226 of 9908 Apples were missing from the APPLES git mirror (backfilled separately, `APPLES` commit `699bdd5`); added `gitSyncMu` + `gitPushWithRetry` (pull --rebase, retry once). Apple #9910, commit `c9217df`.
- docs: VS0–VS13 documentation archaeology — archived the fourteen founder-written KIKORYU founding specs verbatim at `docs/archive/kikoryu-vs-original/` (recovered from `/home/fatbaby/vs0.md`…`vs13.md`); wrote `docs/VS_REALITY_AUDIT.md`, a code-verified SAGA-style (HQ-SPEC-DOC-102) reconciliation of each spec vs. the running system — findings: VS0 identity gate and much of VS1 are live-but-undocumented (device auth, honor code, gamertag, RBAC, event-sourced audit all shipped, absent from NORTHSTAR.md); VS7/VS13/VS12/VS6/VS5/VS4 were reincarnated elsewhere without citation (M2M agent model, mmo.go provenance_chain, DragonsNShit crafting, Stripe subscription rails, stream.go SSE, FATBABY/kgraph ingest); VS3/VS11 superseded by different realities; VS2/VS8/VS9/VS10 unbuilt. Wrote superseding docs in `docs/kikoryu/` (full rewrites for VS0/1/2/7/9/10, status stubs for the rest) oriented to the founder's new direction: social tournaments platform (VS2 primary, VS9+VS10 supporting). All 16 docs registered in EMILY golden-docs-index (VS-REALITY-AUDIT + KIKORYU-VS0…VS13).
- docs: intake `iduna_roadmap.md` (founder-provided, placed at repo-tree root outside any repo) as `docs/NORTHSTAR_KIKORYU.md` — 14-version (VS0–VS13) product roadmap for KIKORYU, the single-shard MMO consumer domain named alongside FATBABY/SECWATCH since IDUNA's original IAM pivot (`iam-spec.md` §1) but never previously given a build plan. Registered in EMILY's golden-docs-index at tier 1. Reformatted for markdown only; content preserved as given.
- fix(store): `RunSQLiteMigrations` translates each migration file's SQL via `mysqlToSQLite` before applying it, but the regexes converting `AUTO_INCREMENT PRIMARY KEY` columns only matched `BIGINT`, not `INTEGER` — `202606250002_mmo_inventory.sql` and `202606250003_monitors.sql` both declare `id INTEGER ... AUTO_INCREMENT PRIMARY KEY`, which translated to invalid SQLite (`AUTOINCREMENT` before `PRIMARY KEY`). Widened `reBigintAutoIncrementPK`/`reBigintAutoIncrementOnly` to match `BIGINT|INTEGER`.
- ops: recovered `var/iduna.db` from a partial application of `202606250002_mmo_inventory.sql` — the 2026-07-16 reboot hard-killed iduna.service mid-migration (no per-statement transaction in `RunSQLiteMigrations`), leaving `items.def_id`/`items.flags` and `character_equipment` applied but unrecorded in `schema_migrations`, so every restart retried from statement 1 and hit `duplicate column name: def_id`. Manually applied the remaining `character_inventory`/`character_key_items`/`character_bag_capacity` tables (matching real `mysqlToSQLite` output) and recorded the migration.

## 2026-07-15

- fix(ops): `scripts/iduna.service` gains an `ExecStartPost` health-check loop (polls `/health` up to 30s) — `Type=simple` previously only guaranteed the process forked, not that the HTTP listener was accepting connections, so `emily-system.service`'s `After=iduna.service` ordering didn't actually mean "IDUNA is ready"

## 2026-06-27
- S138-06: /api/v1/kgraph/query proxy handler (KGraphHandler, KGRAPH_URL); wired with RequireAuth
- S137-03: research_cache table (202606270002) + /api/v1/research/cache CRUD (ResearchHandler)
- S136-02/03: vendors + supply_orders tables (202606270001); /api/v1/supply/ CRUD handler (SupplyHandler)

- S129-05: GET /api/v1/characters/:id/inventory + /equipment endpoints; 4 tests


## 2026-06-25
- feat(monitors): granular RBAC (monitors.read/create/delete/alert/admin), monitor kinds (heartbeat/cron/deadman), GET/:id PATCH/:id POST/:id/recover endpoints, EMILY-PRIME gains monitors.read+create+alert — all tests pass
- Alerting backend: check-in monitors (unique URLs, configurable timeout, site-down Slack+email alerts); monitors migration, IAMStore methods, MonitorsHandler
- migration 202606250002: character_equipment, character_inventory, character_key_items, character_bag_capacity tables; ALTER items ADD def_id + flags

- feat: S128-04 cluster heartbeat — POST /api/v1/agents/heartbeat, GET ?active=true&type=emily_cluster, migration + store impl (Apple #3863)


## 2026-06-24
- feat: S125-05 GET /api/v1/players/{slug}/profile — job+faction_rep+trapx_activity (Apple #3658)
- feat: S127-05 GET/PATCH /api/v1/fieldoffices — in-memory FO snapshot store for district overlay (Apple #3651)
- feat: S126-10 GET /api/v1/players/{slug}/profile — PlayerProfileHandler, display_name/job/fame/last_scene/apples_count, 6 tests (Apple #3554)
- feat: S126-09 per-IP rate limit on auth endpoints — IPRateLimiter 10 req/min, /auth/local + /auth/register wrapped, 429+Retry-After (Apple #3552)
- feat: S126-08 POST /api/v1/auth/refresh — JWT refresh endpoint, RefreshHandler, 7 tests (Apple #3550)
- feat: S125-01 POST /api/v1/auth/register — open GFD registration, free_trial tier, JWT response (Apple #3504)
- feat: S124-02 subscription_tiers migration, GFDTier struct, ListSubscriptionTiers/GetGFDUserTier/SetGFDUserTier/RecordStripeEvent IAMStore methods, /tiers + /stripe webhook handlers (Apple #3497)

## 2026-06-23
- feat: S76-06 PATCH /api/v1/characters/:id/skills (UPSERT skill value, cap 110); GET /api/v1/characters/:id/skills (list all skills)
- feat: S76-04 GET /api/v1/characters/:id/items (list non-destroyed items by owner)
- feat: S76-03 PATCH /api/v1/characters/:id/gold — atomic conditional gold deduction; 409 on insufficient balance

- feat: S75-01 MMO schema (characters/items/guilds/world_events/scene_state migration); S75-02/03/04/05 MMO API handlers (characters CRUD+position, items provenance, guilds, world events); wired into main.go with RequireAuth


## 2026-06-21
- test: S66-01 drive.Client test suite (Apple #2404)
- test: S62-01 auth.Subscription.IsActive() 7-case test suite (Apple #2395)
- test: S56-02 subscriptions handler test suite — 5 tests (Apple #2382)
- test: S56-01 push_tokens handler test suite — 5 tests (Apple #2380)
- test: S53-02 intelligence handler test suite — 4 tests (Apple #2367)
- test: S53-01 HEIMDAL handler test suite — 5 tests (Apple #2365)
- feat: S48-01 GET /api/v1/players leaderboard endpoint (Apple #2338)

- feat: S45-01 POST /api/v1/players/{id}/session stat update endpoint (Apple #2308)


## 2026-06-20
- feat: S43-05 email+password SHANKPIT player auth POST /api/v1/auth/email/{register,login} (Apple #1893)
- feat: S43-03 SHANKPIT Google OAuth flow /api/v1/auth/google/shankpit (Apple #1890)
- feat: S43-02 SHANKPIT player registry — POST/GET /api/v1/players/{register,{id}} (Apple #1888)
- feat: S44-06 GET /api/v1/stream/user-events SSE stream endpoint for Colab (Apple #1882)

- feat: S44-04 GET /api/v1/agents + distributed Emily cluster registry (Apple #1877)


## 2026-06-18
- feat: OpenAPI spec + Python einhorn_sdk + Colab observability (Apple #1446)

- feat: webmaster uid=0, user CRUD, event log + SQLite/MySQL projectors (Apple #1445)


## 2026-06-16

- ApplesHandler: auto-sync every Apple to APPLES git repo via APPLES_GIT_DIR goroutine (Apple #585)


## 2026-06-14
- feat(apples): GET /api/v1/apples/stats/daily-tokens?days=N — daily aggregate token stats from Apple metadata; DailyTokenStat type in auth/types.go; DailyTokenStats store method (SQLite + MySQL); max 90 days; zero-pads missing days; requires apples.read — unblocks MJOLNIR token spend sparkline (M4 complete)
- feat(subscriptions): Emily+ subscription gate (S23-04) — user_subscriptions table (migration 202606140002), UpsertUserSubscription + GetUserSubscription store methods, SubscriptionHandler (/api/v1/subscriptions POST + /me GET), GetEffectivePermissions now appends cap.query.full for active subscribers, EDIS-WOOCOMMERCE agent registered (subscriptions.admin)
- feat(drive): Google Drive API integration — internal/drive/client.go (stdlib-only service account auth: RS256 JWT → Bearer token → Drive v3 REST), DriveHandler (/api/v1/drive/upload, /api/v1/drive/files, /api/v1/drive/files/{id}); drive.write + drive.read permissions; degraded-mode 503 when GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON not set
- feat(agents): EMILY-TRAINING agent registered (drive.write, drive.read, apples.write/read) — drives GPT-2 fine-tuning pipeline
- migration: 202606140001_drive_sync_log.sql — Drive sync audit table
- feat(env): GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON + GOOGLE_DRIVE_FOLDER_ID env vars wired into main.go

## 2026-06-03

### Documentation rereview — IAM/API alignment

- Rewrote `openapi.yaml` around the implemented IAM surface: Google ID token exchange, agent M2M exchange, JWKS, `/api/v1/identities/me`, Apples, and Back Office entry points.
- Refreshed `README.md` into a current project overview and documentation index.
- Marked the IAM and Apples implementation checklists complete in repository, with live Apple publication called out as a deployment-time verification step.

### Bootstrap: config-as-code agent provisioning

**Problem:** No way to bring IDUNA online without manually setting up agent permissions in the admin UI. IDUNA needs MySQL → Bob needs IDUNA → classic chicken-and-egg.

**Solution:** `cmd/bootstrap` — a narrow, one-shot CLI tool (no LLM, no HTTP server) that:
1. Runs all pending DB migrations
2. Seeds agent permissions from `config/agents.json`
3. Generates API key secrets for any agents not yet provisioned
4. Writes secrets to `var/agent-secrets.env`

**`config/agents.json`** — declarative, git-committed definition of all system agents (EMILY-PRIME, FATBABY-EMILY, EMIREE, JON, BOB) and their minimum-necessary permissions. Edit + re-run bootstrap to change an agent's authority. No admin UI required.

**`migrations/truestore/202606030001_system_seeds.sql`** — new migration seeding:
- System owner user (`system@einhorn.internal`) for agent FK constraint
- System agent stubs with fixed deterministic UUIDs
- New agent-scoped permissions: `fatbaby.operator`, `emily-prime.operator`, `emiree.super`, `bob.db.admin`, `signalapi.read`, `jon.setups.write`

**Startup sequence** (documented in README):
```
go run ./cmd/bootstrap   # migrate + seed + generate secrets
source var/agent-secrets.env
go run .                  # start IDUNA
go run ./cmd/bob-agent    # Bob comes online
# then: start FATBABY-EMILY, JON, EMILY-PRIME with their IDUNA credentials
```

**`var/agent-secrets.env`** is git-ignored. Each agent's env var is `IDUNA_SECRET_<AGENTNAME>`.

Bootstrap is idempotent: safe to re-run on every deploy. Pass `-rotate` to regenerate all secrets.

## 2026-06-02

### HQ-SPEC-IAM-096 — Apples: Golden Documentation Log Streaming

Apples are structured records emitted by agents at the end of each recursive
self-improvement run. They form the paper trail that closes the RSI loop.

**Database**
- Migration `202606020001_apples.sql`: `apples` table (append-only, FK to agents)
- Permissions seed: `apples.write`, `apples.read`, `apples.admin`
- super_admin and analyst role assignments

**Store**
- `auth.AppleRecord` type added to `internal/auth/agent.go`
- `IAMStore` interface: `AppendApple`, `ListApples`, `GetApple`
- `MySQLStore` implementation: `AppendApple` runs in a transaction that also
  emits `ApplePublished` to `iam_event_stream`

**API**
- `POST /api/v1/apples` — submit a new Apple (requires `apples.write`)
- `GET  /api/v1/apples` — list Apples with filters (requires `apples.read`)
- `GET  /api/v1/apples/{id}` — full Apple with body and metadata (requires `apples.read`)
- Auth: Bearer JWT from existing M2M agent auth flow

**Admin UI (Back Office)**
- `/admin/apples` — filterable ledger (source_repo, agent_id, apple_type)
- `/admin/apples/{id}` — full detail view: body as preformatted text, metadata JSON block
- Nav bar updated with Apples link

**Tests**
- 9 handler tests covering: success create, missing permission, missing fields,
  no auth, list, filter by repo, get by id, not found, apples.admin permission

---

## 2026-06-01

### HQ-SPEC-IAM-095 — Agent M2M credential authentication

- `POST /api/v1/auth/agent` credential exchange endpoint
- Migration: `api_key_hash` column on agents table
- `SetAgentCredential` / `AuthenticateAgent` store methods
- `/api/v1/jwks` endpoint

### Back Office admin UI

- `/admin` overview, `/admin/users`, `/admin/agents`, `/admin/audit`
- IAM events audit log viewer
