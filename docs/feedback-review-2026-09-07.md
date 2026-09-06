# Feedback review — 2026-09-07

Scope: the first 146 local feedback entries, through 2026-09-05, reviewed
against baseline `e192418` on the existing branch. IDs are original 1-based
log line numbers, not issue numbers. No original records were removed or
marked resolved. Session IDs and raw private URLs are not copied here.
The additional local verification record #147 (missing mouse CLI help) was
recorded during this review and is covered by the mouse CLI/MCP change.

「既有處理」表示程式碼及測試已有對應處理，不代表重播過原網站。
「待重現」不算已解決，需要可重現頁面、操作順序及 DOM/event 證據。
功能缺口有列明，沒有以未實作的 API 冒充修正。

主要確認根因包括：jq 部分 parser 把不支援語法視為 identity；extension
忽略 jq；輸入與 URL 確認文字洩漏值；snapshot 讀初始 attribute；
ARIA-only checkbox 被直接改狀態；Monaco helper input 被誤當整個模型；
CDP profile status 使用 managed identity/port。

相容性變動：jq 現採標準 missing-field/null 與型別語意；無效 filter
在操作前拒絕，runtime error 丟棄整批結果。fill/type 不再回顯值。
本次沒有加入 full-page screenshot、CSS media emulation、trusted tap 或
任意 cross-origin frame execution context。

## 逐筆判定

| ID | 判定 | 證據／處置 |
| --- | --- | --- |
| 1 | 既有處理 | WebAuthn 已有虛擬 authenticator；doctor 可辨識 binary drift。 |
| 2 | 非待修問題 | 原文明示為 feedback 寫入驗證資料，未刪除。 |
| 3 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 4 | 外部限制 | WebAuthn 已存在；sandbox home 寫入限制不可由 borz 繞過，可允許 runtime 或使用另一個 BORZ_HOME。 |
| 5 | 外部限制 | WebAuthn 已存在；sandbox home 寫入限制不可由 borz 繞過，可允許 runtime 或使用另一個 BORZ_HOME。 |
| 6 | 外部限制 | WebAuthn 已存在；sandbox home 寫入限制不可由 borz 繞過，可允許 runtime 或使用另一個 BORZ_HOME。 |
| 7 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 8 | 既有處理 | client 健康連線 fast path 在 runtime 建立與 startup lock 之前。 |
| 9 | 既有處理 | WebAuthn 已有虛擬 authenticator；doctor 可辨識 binary drift。 |
| 10 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 11 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 12 | 既有處理 | client 健康連線 fast path 在 runtime 建立與 startup lock 之前。 |
| 13 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 14 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 15 | 本次改善／待原站驗證 | fetch 錯誤加入 CORS/cookie/frame 診斷；文件說明 resource history 不等於可重播的認證請求。 |
| 16 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 17 | 本次修正 | 嵌入 gojq 支援 map/select/generator/排序/切片；extension/local JSON 也套用；空結果不改根重試，錯誤不輸出原始資料。 |
| 18 | 本次修正 | catalog 不印無關 metadata 錯誤；site lint 路徑仍可診斷。#18 的 history timeout 仍需現場重現。 |
| 19 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 20 | 既有處理 | navigate 的 commandUsageHint 已提示 borz --tab <id> open <url>。 |
| 21 | 既有處理 | text-only snapshot 保留可操作 refs。 |
| 22 | 既有處理 | tab/tabs alias 已存在。 |
| 23 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 24 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 25 | 既有處理 | modifier parsing 與 CDP selectAll editing commands 已存在。 |
| 26 | 功能建議 | CSS reduced-motion/color-scheme emulation 的缺口屬實；本次未新增此 API。 |
| 27 | 既有處理 | screenshot --annotate 已存在。 |
| 28 | 既有處理 | daemon token --copy、extension ping/all-profiles、固定 port/token 與 profile mismatch 檢查已存在。 |
| 29 | 既有處理 | daemon token --copy、extension ping/all-profiles、固定 port/token 與 profile mismatch 檢查已存在。 |
| 30 | 既有處理 | daemon token --copy、extension ping/all-profiles、固定 port/token 與 profile mismatch 檢查已存在。 |
| 31 | 既有處理 | daemon token --copy、extension ping/all-profiles、固定 port/token 與 profile mismatch 檢查已存在。 |
| 32 | 既有處理 | daemon token --copy、extension ping/all-profiles、固定 port/token 與 profile mismatch 檢查已存在。 |
| 33 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 34 | 功能建議 | CSS reduced-motion/color-scheme emulation 的缺口屬實；本次未新增此 API。 |
| 35 | 既有處理 | captureScreenshot 以臨時 style 隱藏 overlay；有 screenshot E2E。 |
| 36 | 既有處理／待原站驗證 | backend/shadow node ref 解析已有處理；未重播 chrome://extensions 原始個案。 |
| 37 | 功能建議 | CSS reduced-motion/color-scheme emulation 的缺口屬實；本次未新增此 API。 |
| 38 | 功能建議 | click 目前仍是滑鼠事件，沒有 trusted tap 指令；本次未新增 touch API。 |
| 39 | 既有處理 | 保留 focus-only URL reuse；已明示 Reused/not reloaded 並提示 refresh。 |
| 40 | 既有處理 | 保留 focus-only URL reuse；已明示 Reused/not reloaded 並提示 refresh。 |
| 41 | 既定語意 | viewport 為 per-tab；open/tab new 可帶 --viewport，不全域覆寫其他分頁。 |
| 42 | 既定語意／已有診斷 | RefInvalidationReason 已說明導覽原因；導覽後必須重新 snapshot，不能猜測新頁的舊 ref。 |
| 43 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 44 | 待重現 | 已有 clipboard permission grant/bringToFront；原 macOS focus 與 clipboard session 證據不足。 |
| 45 | 既有處理／待原站驗證 | 已有明確 tab selection、穩定順序、tab pinning 與 about:blank reconciliation tests；未重播原 profile。 |
| 46 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 47 | 本次修正 | 嵌入 gojq 支援 map/select/generator/排序/切片；extension/local JSON 也套用；空結果不改根重試，錯誤不輸出原始資料。 |
| 48 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 49 | 既有處理 | tab/tabs alias 已存在。 |
| 50 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 51 | 既有處理 | tab/tabs alias 已存在。 |
| 52 | 既有處理 | upload、associated file input、filechooser accept 與 help 已存在。 |
| 53 | 既有處理 | text-only snapshot 保留可操作 refs。 |
| 54 | 既有處理 | internal/jseval 已處理 await、return 與 lexical scope。 |
| 55 | 既有處理 | 保留 focus-only URL reuse；已明示 Reused/not reloaded 並提示 refresh。 |
| 56 | 功能建議 | 現有 frame 命令不等於任意 cross-origin/OOPIF execution-context API；本次未宣稱補齊。 |
| 57 | 功能建議 | 現有 frame 命令不等於任意 cross-origin/OOPIF execution-context API；本次未宣稱補齊。 |
| 58 | 既有處理 | upload、associated file input、filechooser accept 與 help 已存在。 |
| 59 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 60 | 既有處理／待原站驗證 | 已有明確 tab selection、穩定順序、tab pinning 與 about:blank reconciliation tests；未重播原 profile。 |
| 61 | 既有處理／待原站驗證 | native setter/input/change 已存在；本次加 composed events 與值拒絕檢查，原站登入流程未重播。 |
| 62 | 既有處理 | 已有 navigation/selector timeout 與 protocol timing 的 client deadline 配置。 |
| 63 | 既有處理 | upload、associated file input、filechooser accept 與 help 已存在。 |
| 64 | 非 borz 問題 | 屬 rtk find 的 CLI 行為，不修改 borz。 |
| 65 | 本次修正 | date 不再呼叫不支援的 selection range；拒絕格式回錯。Chrome fixture 驗證 ISO 日期。 |
| 66 | 既有處理／待原站驗證 | CSS-scoped tree 與 rendered-name fallback 已存在；原 React Flow/SAP DOM 未重播。 |
| 67 | 既有處理 | modifier parsing 與 CDP selectAll editing commands 已存在。 |
| 68 | 既有處理 | 現有 WxH 用法與錯誤提示已說明尺寸語法。 |
| 69 | 既有處理／待原站驗證 | CSS-scoped tree 與 rendered-name fallback 已存在；原 React Flow/SAP DOM 未重播。 |
| 70 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 71 | 既有處理／待原站驗證 | CSS-scoped tree 與 rendered-name fallback 已存在；原 React Flow/SAP DOM 未重播。 |
| 72 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 73 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 74 | 既有處理／待原站驗證 | 已有明確 tab selection、穩定順序、tab pinning 與 about:blank reconciliation tests；未重播原 profile。 |
| 75 | 本次修正／待原站驗證 | 禁止僅改 aria-checked 造成假成功；保留 native 與明確 checked/fireChange API。Chrome fixture 驗證 inert checkbox 不會變 true。 |
| 76 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 77 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 78 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 79 | 既有處理 | get value 已讀 textarea live property，有測試。 |
| 80 | 既定語意 | get 第二參數是 ref；全文可用 snapshot --text-only 或 get text 不帶 ref。 |
| 81 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 82 | 既有處理 | upload、associated file input、filechooser accept 與 help 已存在。 |
| 83 | 本次修正 | network list 現為 network requests alias。 |
| 84 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 85 | 既定語意 | press 是 page CDP key event，不控制網址列；敏感 URL stdin 為新功能建議。 |
| 86 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 87 | 本次修正 | fill/type 支援 --file，CLI/MCP/daemon response 不回顯輸入；CLI 也遮蔽舊 daemon Value。 |
| 88 | 本次修正 | 明確空字串/空檔可清空 clipboard；REST 仍拒絕 omitted/null text。 |
| 89 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 90 | 既有處理／待原站驗證 | 已有明確 tab selection、穩定順序、tab pinning 與 about:blank reconciliation tests；未重播原 profile。 |
| 91 | 待重現 | 已有 clipboard permission grant/bringToFront；原 macOS focus 與 clipboard session 證據不足。 |
| 92 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 93 | 本次修正 | fill/type 支援 --file，CLI/MCP/daemon response 不回顯輸入；CLI 也遮蔽舊 daemon Value。 |
| 94 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 95 | 既有處理 | select 已支援 native 與 ARIA/component combobox，並驗證所選值。 |
| 96 | 既有處理 | upload、associated file input、filechooser accept 與 help 已存在。 |
| 97 | 待重現 | 已有 backend node/document invalidation；缺原站 DOM 更新事件，不安全自動換成另一個同名控制項。 |
| 98 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 99 | 待重現 | 已有 backend node/document invalidation；缺原站 DOM 更新事件，不安全自動換成另一個同名控制項。 |
| 100 | 待重現 | 已有 backend node/document invalidation；缺原站 DOM 更新事件，不安全自動換成另一個同名控制項。 |
| 101 | 待重現 | 已有 backend node/document invalidation；缺原站 DOM 更新事件，不安全自動換成另一個同名控制項。 |
| 102 | 既有處理 | navigate 的 commandUsageHint 已提示 borz --tab <id> open <url>。 |
| 103 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 104 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 105 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 106 | 既有處理／待原站驗證 | 已有明確 tab selection、穩定順序、tab pinning 與 about:blank reconciliation tests；未重播原 profile。 |
| 107 | 既有處理 | daemon recovery、保留 Chrome 與 EPERM 指引已有實作；版本不符依規範只警告，不自動重啟。 |
| 108 | 既有處理／待原站驗證 | 已有 descendant 命中、combobox 容器、hidden terminal focus、framework click 與 custom select；未重播原站。 |
| 109 | 本次修正 | CDP status 依宣告 endpoint 探測並標示外部 ownership；profile purge 不會關閉外部 CDP browser。 |
| 110 | 本次修正 | CDP status 依宣告 endpoint 探測並標示外部 ownership；profile purge 不會關閉外部 CDP browser。 |
| 111 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 112 | 本次修正／待原站驗證 | 禁止僅改 aria-checked 造成假成功；保留 native 與明確 checked/fireChange API。Chrome fixture 驗證 inert checkbox 不會變 true。 |
| 113 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 114 | 本次修正 | unknown use 直接提示 tab select --id。 |
| 115 | 本次改善／待原站驗證 | CLI 空 snapshot 提供載入/filter/screenshot 診斷；原站空樹根因未重現。 |
| 116 | 待重現 | DOM value 不等於 SAP model/server 接受；未重播 mandatory validation 流程，不能宣稱修復。 |
| 117 | 本次修正 | 實際嵌入 walker 讀 live property；textarea 不顯示舊 child text，password 值遮蔽。 |
| 118 | 本次補齊 | 公開既有 mouse CLI/MCP：down、move --button left、up 可做 canvas pointer drag；非 HTML5 DataTransfer。 |
| 119 | 網站語意待驗證 | Decline 可能來自網站 aria-label/title；沒有足夠證據讓 borz 擅自改成 Close。 |
| 120 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 121 | 本次修正 | 拒絕用 Monaco hidden textarea 冒充整個 model，提供 focus/select-all/type 替代。 |
| 122 | 本次修正 | CLI tab/tabs text/JSON 遮蔽 userinfo、credential query/fragment，不改導覽 URL。 |
| 123 | 本次修正 | fill/type 支援 --file，CLI/MCP/daemon response 不回顯輸入；CLI 也遮蔽舊 daemon Value。 |
| 124 | 待重現 | 已有 backend node/document invalidation；缺原站 DOM 更新事件，不安全自動換成另一個同名控制項。 |
| 125 | 本次修正 | CLI tab/tabs text/JSON 遮蔽 userinfo、credential query/fragment，不改導覽 URL。 |
| 126 | 本次修正 | text-only snapshot 以明示 marker 省略 Monaco 虛擬化 internals。 |
| 127 | 本次修正 | 嵌入 gojq 支援 map/select/generator/排序/切片；extension/local JSON 也套用；空結果不改根重試，錯誤不輸出原始資料。 |
| 128 | 本次修正 | catalog 不印無關 metadata 錯誤；site lint 路徑仍可診斷。#18 的 history timeout 仍需現場重現。 |
| 129 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 130 | 本次修正 | reload 對應 refresh。 |
| 131 | 本次修正 | catalog 不印無關 metadata 錯誤；site lint 路徑仍可診斷。#18 的 history timeout 仍需現場重現。 |
| 132 | 本次修正 | 支援 screenshot --output；未知 flag/多路徑在寫檔前拒絕，--full-page 仍未支援且明確報錯。 |
| 133 | 既有處理／待原站驗證 | 已有 tab front 與 page visibility override，不保證所有 OS compositor/動畫狀態。 |
| 134 | 本次修正 | missing ref 錯誤顯示 positional 用法與範例。 |
| 135 | 既有處理 | daemon/CLI 已拒絕空 image data，local-write 有測試；原 profile 個案未重播。 |
| 136 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 137 | 本次修正 | 嵌入 gojq 支援 map/select/generator/排序/切片；extension/local JSON 也套用；空結果不改根重試，錯誤不輸出原始資料。 |
| 138 | 本次修正 | 嵌入 gojq 支援 map/select/generator/排序/切片；extension/local JSON 也套用；空結果不改根重試，錯誤不輸出原始資料。 |
| 139 | 本次改善／待原站驗證 | fetch 錯誤加入 CORS/cookie/frame 診斷；文件說明 resource history 不等於可重播的認證請求。 |
| 140 | 既定語意／已有診斷 | RefInvalidationReason 已說明導覽原因；導覽後必須重新 snapshot，不能猜測新頁的舊 ref。 |
| 141 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 142 | 本次修正 | 嵌入 gojq 支援 map/select/generator/排序/切片；extension/local JSON 也套用；空結果不改根重試，錯誤不輸出原始資料。 |
| 143 | 既有處理 | navigate 的 commandUsageHint 已提示 borz --tab <id> open <url>。 |
| 144 | 本次修正 | wait 明示 milliseconds 語法及 action --wait-for selector。 |
| 145 | 待重現 | 有共用 hit-test、shadow、framework fallback 與等待機制；未保存原站當時 DOM/遮罩，不能宣稱個案已修復。 |
| 146 | 本次補齊 | 公開既有 mouse CLI/MCP：down、move --button left、up 可做 canvas pointer drag；非 HTML5 DataTransfer。 |

## Validation

- `go vet ./...`
- `go test -race -coverprofile=... -covermode=atomic ./...`: passed; total coverage 87.6%.
- Real Chrome, using the borz CLI helper: `TestE2EFeedbackRegressions`,
  `TestE2ECLICommandsAgainstVerifySite` (six subtests),
  `TestE2ECLIOutputShaping`, and `TestE2ECLIScreenshotOutput`: passed.
- Regression fixtures verify no input echo, jq filtering/empty selections/error
  handling, date input, live snapshot values, inert checkbox rejection, Monaco
  refusal/omission, pointer clicks and held-button movement, explicit clipboard
  clearing, CDP profile liveness, and external browser ownership during purge.

Tests use local synthetic pages, not the user's customer sites. No existing
shared daemon was restarted and no installed global binary was replaced.

Deployment follow-up: the installed-binary smoke check found that `status`,
`daemon status`, and `server status` still used direct JSON formatting.
These now route the formatted payload through the shared jq output boundary;
`TestFeedbackStatusCommandsHonorJQ` covers all three entry points.
