# Tool-schema depth clamp + upstream error visibility — รายงาน

วันที่: 2026-10-09 · branch: `fix/tool-schema-depth-clamp` · base: `master` (`1efe7d1`)

---

## สรุปสั้น

เจอปัญหาจากการใช้งานจริงผ่าน ZCode: ทุก request ที่ส่ง tool ซึ่งมี JSON schema ซ้อนลึกเกิน 10 ชั้นจะถูก upstream ปฏิเสธทั้งชุด ด้วยข้อความ `JSON schema exceeds the maximum nesting depth of 10 levels` — ทั้งที่แผง Test Model ใน admin console ผ่านปกติ ทำให้เข้าใจผิดว่าตัว proxy ปกติ

รายงานนี้แยกเป็น 4 หัวข้อ พร้อมระบุว่า **แก้อะไร** และ **ถ้าไม่แก้จะเป็นยังไง** รวมถึงทางเลือกอื่นที่พิจารณาแล้วไม่เลือก พร้อมเหตุผล

---

## วิธีตรวจ (หลักฐาน)

1. **อ่าน telemetry ของ server ตัวเอง** ผ่าน `/admin/api/requests` — entry ของ request ที่ล้มเหลว (seq 2961) แสดง `attempts` สองตัว: `opencode → 400 nesting depth` แล้ว `cline → all keys exhausted`
2. **ยิง probe ควบคุมความลึก** เข้า `172.245.12.10:1765` โดยส่ง tool หนึ่งตัวที่ schema ซ้อน N ชั้น:

| ความลึก | ผลลัพธ์ |
|---|---|
| 6, 8, 10 ชั้น | `200` ตอบถูกต้อง |
| 12, 16, 24 ชั้น | `400 JSON schema exceeds the maximum nesting depth of 10 levels` |

3. **ยิงซ้ำแบบ `stream: true`** เพื่อดูของดิบจาก leg สำรอง → cline ตอบ `200` แล้วส่ง SSE มา **หนึ่งเฟรมที่เป็น error**:

```
data: {"error":{"code":"stream_initialization_failed","message":"Failed to create stream: inference request failed:
 failed to generate stream from OpenRouter: failed to invoke model 'meta/muse-spark-1.3-contributor' ...
 \"JSON schema exceeds the maximum nesting depth of 10 levels\" ...","type":"stream_error"}}
```

จากนั้นจึงยืนยันได้ว่า **ทั้งสองเส้น (opencode และ cline ซึ่งยิงต่อ OpenRouter → Meta) ปฏิเสธ payload เดียวกัน** — ปัญหาอยู่ที่สิ่งที่ proxy ส่งออก ไม่ใช่ provider เฉพาะ และโค้ดเราไม่ได้เพิ่มความลึกเอง (`adapter.responses.go` copy `parameters` ตรง ๆ)

---

## หัวข้อ 1 — upstream ตี schema ลึกเกินลิมิต

### ปัญหาคืออะไร

Gateway ฝั่ง upstream validate `parameters` ของ **ทุกตัวใน array** ถ้าตัวเดียวลึกเกินลิมิต (opencode: 10) ทั้ง request จะโดน 400 ไม่ใช่แค่ตัวนั้นถูกกรอง ZCode ส่ง MCP tool set เต็มซึ่งมีตัวที่ลึกเกิน (เช่น schema ที่มี `requestBody → oneOf → items → properties → …` ซ้อนกันหลายชั้น) ส่วนแผง Test Model ไม่ติดเพราะสร้าง request ของตัวเอง**โดยไม่มี tools เลย** ไม่ใช่เพราะ path ต่างกัน

### แก้อะไร

- `internal/services/schemaclamp.go` (ใหม่) — เดินต้นไมอ schema ของทุก tool แบบ depth-first แล้วแทน node ที่อยู่ที่เพดานด้วย stub ที่เก็บ `type`/`title`/`description`/`format`/`enum`/`default` → **ชื่อ property และคำอธิบายยังอยู่** โครงสร้างลึกกว่านั้นหาย
- จุดเรียก: `translateRoute` ก่อน dialect dispatch — จุดเดียวครอบทั้ง chat/responses/messages เพราะทุก dialect แปลงต่อจาก `parameters` ตัวเดิม
- ตัวเลือก: ต่อ leg `clamp_tool_schemas: true` (พี่น้องของ `sanitize_tool_names`) + ระดับบน `clamp_tool_schemas: true` และ `tool_schema_max_depth` (default **8** ผ่าน `config.DefaultToolSchemaMaxDepth`)
- log ทุกครั้งที่ clamp: `route <model>/<provider>: flattened N tool schema(s) deeper than D levels: <ชื่อ tool>` — เป็นช่องทางเดียวที่จะเห็นว่า tool ของ client ตัวไหนลึกเกิน เพราะเราไม่เก็บ request body
- **default = OFF** เพราะการ flatten คือการลดคุณภาพ schema ทั้งระบบ ควรเป็นการตั้งใจเปิด

### ถ้าไม่แก้

- ทุก session ที่ client ส่ง tool ลึกเกินจะ fail ทั้งชุด แก้ได้ทางเดียวคือปิด MCP server ที่มี schema ลึก หรือลด tool set ฝั่ง client — และจะเจอซ้ำทุก provider ที่บังคับลิมิตเดียวกัน
- อาการที่ทำให้ debug ยาก: Test Model ยังผ่าน ทำให้เข้าใจว่า "ตัวเราปกติ" ทั้งที่ปัญหาอยู่ที่ payload

### ทางเลือกอื่นที่พิจารณาแล้วไม่เลือก

| ทางเลือก | เหตุผลที่ไม่เลือก |
|---|---|
| ตัด tool ที่ลึกทิ้งทั้งตัว | ตรงไปตรงมาแต่ client จะเรียกใช้ MCP ตัวนั้นไม่ได้อีกเลย (เสีย capability ถาวรจนกว่าจะปิด clamp) |
| แก้ที่ client (ZCode) | ฝั่งเราควบคุมไม่ได้ และ client ควรเผื่อ gateway หลายเจ้าที่มี limit ต่างกัน — ฝั่ง proxy แก้ครั้งเดียวจบ |
| ไม่ทำอะไร ปล่อยให้ upstream ตัดสิน | ข้อเสียคือ debug ยากและผู้ใช้จะเจอ error ที่ไม่มีทางแก้จากฝั่งเรา |
| ตั้ง default เป็น ON | ตัด schema ของทุกคนโดยไม่ได้ขอ ทั้งที่ผลกระทบคือ model มองโครงสร้างลึกไม่เห็น |

### ความเสี่ยงที่เหลือ

tool ที่ถูก flatten ยังถูกเรียกได้ แต่ model มองโครงสร้างลึกไม่เห็น → อาจส่ง argument ผิด ตรวจได้จาก log ที่เพิ่ม ถ้าพบว่ากระทบคุณค่อยเปลี่ยนกลยุทธ์เป็นตัดทิ้ง (แก้จุดเดียวใน `schemaclamp.go`)

---

## หัวข้อ 2 — gateway รับ `tool_choice` แค่ `"auto"`

### ปัญหาคืออะไร

ระหว่าง probe เจอว่า gateway ตอบ `only "auto" is supported for tool_choice. "none", "required", and named function choices are not currently supported` โค้ดเดิม inject `"auto"` เฉพาะตอน client ไม่ส่ง ถ้า client ส่งค่าอื่นจะถูกส่งตรงไปและโดน 400 (telemetry seq 3132-3134)

### แก้อะไร

เฉพาะ leg ของ opencode: `forceAutoToolChoice` แปลง `"required"`/ระบุชื่อฟังก์ชัน → `"auto"` พร้อม log เตือนหนึ่งบรรทัด ส่วน `"none"` ซึ่งแปลว่า client ไม่ต้องการให้เรียก tool เลย → `dropToolUse` ลบ `tools` + `tool_choice` ทิ้งทั้งคู่ (ทำ**หลัง** inject ชุด required เสร็จ ไม่งั้นถูก inject กลับเข้ามา) ครอบทุก dialect ของ provider นี้

### ถ้าไม่แก้

client ที่ส่ง `tool_choice: none` จะโดน 400 ทุกครั้งบนเส้น opencode แล้วไหลไป leg สำรอง (ช้าลง อาจหมด key) หรือถ้าไม่มี leg สำรองก็ได้ error ตรง ๆ

### ทางเลือกอื่นที่พิจารณาแล้วไม่เลือก

| ทางเลือก | เหตุผลที่ไม่เลือก |
|---|---|
| บังคับ `"auto"` ทั้งหมดรวม `"none"` | ทำให้ model เรียก tool ที่ client ตั้งใจปิด — ผิดความหมายของ client |
| ปล่อยให้ client จัดการเอง | ควบคุมไม่ได้ และอาการจะกลับมาเป็น 400 ที่ debug ยากเหมือนเดิม |

---

## หัวข้อ 3 — error จริงถูกกลืนเมื่อ upstream ตอบ 200 + error frame

### ปัญหาคืออะไร

บาง gateway ตอบ `200` แล้วค่อย fail ที่การเรียกโมเดล โดยส่ง error frame มาใน stream — cline relay ของ OpenRouter/Meta เป็นแบบนี้ ผลที่เกิดกับ proxy:

- **client แบบ stream**: เห็น error frame ตามปกติ (ผ่าน passthrough)
- **client แบบ non-stream**: `bufferStream` แปลงเฟรมเป็น `model.StreamChunk` ซึ่งไม่มี field `error` → json unmarshal ได้ ไม่ error แต่ไม่มี content → ออกเป็น `chat.completion` เปล่า `choices: []` ด้วย **200**
- **telemetry ก็ไม่จับ**: บันทึก `status 200, tokens 0, error: null` เพราะ path นี้ไม่รู้ว่ามี error แฝงอยู่

นี่คือเหตุผลที่การหาสาเหตุตอนแรกเสียเวลาไปกับการตามหา 400 ตัวจริงที่ถูกกลืนไปแล้ว

### แก้อะไร

- `util.ParseErrorFrame` (ใหม่ใน `internal/util/sse.go`) — seam ตัวเดียวที่ Task 3 และ Task 4 ใช้ร่วมกัน อ่านว่า payload เป็น error frame หรือไม่
- `bufferStream` จับเฟรม error แรกไว้ และคืนมาเป็น failure **เมื่อ stream ไม่ได้ให้ content เลย** → `serveOpenAI` ตอบ `502 upstream_error` พร้อมข้อความ sanitized (ไม่สะท้อนข้อความ upstream ให้ client ตามนโยบายเดิม) ส่วน payload ดิบไปอยู่ใน log + telemetry
- **content ที่มาถึงก่อน error frame ยังชนะ** — ถ้ามี output จริงแล้ว ไม่ถูกแทนด้วย error
- client แบบ non-stream ที่โดน 502 จะเห็นสาเหตุใน `/admin/api/requests` ทันที

### ถ้าไม่แก้

อาการ "ตอบเปล่า" จะกลายเป็นเรื่องที่ debug ยากที่สุดเวลา provider มีปัญหา เพราะทุกชั้น (client, log, telemetry) บอกว่า "ปกติ"

### ทางเลือกอื่นที่พิจารณาแล้วไม่เลือก

| ทางเลือก | เหตุผลที่ไม่เลือก |
|---|---|
| หยุด fallback ทันทีเมื่อ leg แรกตอบ 4xx | เสีย resilience ที่ดีอยู่แล้ว (opencode 400 แล้วให้ cline ต่อคือพฤติกรรมที่ถูกต้อง) |
| บันทึก log/telemetry เพิ่ม แต่ยังส่ง shell เปล่าให้ client | client ยังเห็นอาการที่เข้าใจไม่ได้ ขณะที่แก้ได้จริงในระดับ buffer |
| แก้ครบทุก dialect ในรอบเดียว | buffer sink ของ responses (stub ทิ้งข้อความ), google (stub) และ anthropic (ไม่มี case) มีอาการเดียวกัน แต่ยังไม่ได้พิสูจน์ว่าเกิดจริง — ขยาย scope เพื่อของที่ยังไม่เจอ จึงบันทึกไว้เป็นงานรอบถัดไปแทน |

### ข้อจำกัด

- client แบบ **stream** แก้ไม่ได้ (ส่ง header 200 ออกไปแล้วเรียกคืนไม่ได้) — พฤติกรรมเดิม
- `util.StreamRawSSE` (เส้นทาง `/v1/messages` dialect messages) ยังส่งตรง ๆ

---

## หัวข้อ 4 — ข้อมูลภายในของ upstream รั่วผ่าน stream

### ปัญหาคืออะไร

เฟรม error ที่ cline ส่งมามีข้อมูลภายในติดไปด้วย: ชื่อ provider (`Meta`, `OpenRouter`), `user_id: org_2ue3s...`, `request_id` และชื่อโมเดลจริง (`meta/muse-spark-1.3-contributor`) และเฟรมนี้ถูกส่งต่อถึง client ทุกคนที่ใช้ stream ขณะที่เส้น non-stream ถูก sanitize แล้ว — ไม่สม่ำเสมอ

### แก้อะไร

`util.StreamSSETransform` แทนที่ทุก error frame ด้วย `util.SanitizedErrorFrame` — รูปแบบ `{"error":{…}}` เดิมที่ client parser รองรับอยู่แล้ว แต่ไม่มีชื่อ provider/account id/model mapping ของ upstream ติดไป เฟรมปกติและ `[DONE]` ไม่เปลี่ยน

### ถ้าไม่แก้

ข้อมูลภายในของ upstream (ชื่อผู้ให้บริการรายย่อย, account id, โครงสร้างเส้นทางโมเดล) รั่วถึงลูกค้าทุกรายที่ใช้ stream ซึ่งอาจเป็นข้อมูลที่ไม่ควรเผยแพร่ต่อ

### ทางเลือกอื่นที่พิจารณาแล้วไม่เลือก

| ทางเลือก | เหตุผลที่ไม่เลือก |
|---|---|
| ปล่อยไว้รอบหน้า | ปล่อยให้รั่วต่อไปเรื่อย ๆ โดยไม่มีเหตุผล |
| ตัด error frame ทิ้ง | client จะได้ stream ที่จบแบบเงียบ ๆ แย่กว่า — อย่างน้อยต้องบอกว่ามี error |

### ข้อจำกัด

error frame ที่ adapter สร้างเอง (codex, google) ยังส่งข้อความ upstream verbatim อยู่ — เป็นอีกผิวหนึ่งของเรืองเดียวกัน แต่ไม่ได้อยู่ใน scope รอบนี้

---

## วิธีเปิดใช้งาน

```yaml
# routerllm.yaml — ระดับบน (ทุก leg)
clamp_tool_schemas: true
tool_schema_max_depth: 8      # optional, ค่า default คือ 8
```

หรือเฉพาะ leg:

```yaml
routes:
  - model_id: muse-spark-1.3-contributor
    routes:
      - provider: opencode
        model: muse-spark-1.3-contributor-free
        stylecall: responses
        clamp_tool_schemas: true
```

**ต้อง rebuild + restart binary** — การเปลี่ยนทั้งหมดอยู่ในโค้ด ไม่ใช่ config (ส่วนสองคีย์ใหม่รีโหลดเองได้ถ้าเปิดหลัง restart)

### วิธีตรวจว่าได้ผล

ดูบรรทัด log `flattened N tool schema(s) deeper than D levels: <ชื่อ tool>` เมื่อมีการ clamp — จะเห็นทันทีว่า tool ของ client ตัวไหนลึกเกิน (ก่อนหน้านี้หาไม่ได้เลย เพราะไม่มี request body ให้ดู)

---

## ไฟล์ที่เปลี่ยน

| ไฟล์ | การเปลี่ยน |
|---|---|
| `internal/services/schemaclamp.go` | ใหม่ — ตัวนับ depth + การ flatten |
| `internal/services/schemaclamp_test.go` | ใหม่ — เทสต์หน่วย + integration ผ่าน `Forward` |
| `internal/services/proxy.go` | field/setter ของ clamp, จุดเรียกใน `translateRoute`, `forceAutoToolChoice`/`dropToolUse`, `bufferStream` คืน error, `serveOpenAI` เป็น method |
| `internal/services/proxy_stream_error_test.go` | ใหม่ — error frame บน buffering |
| `internal/util/sse.go` | `ParseErrorFrame`, `SanitizedErrorFrame`, sanitize ใน `StreamSSETransform` |
| `internal/util/sse_test.go` | ปรับเทสต์เดิม + เพิ่มเทสต์ sanitize |
| `internal/model/model.go`, `internal/provider/provider.go` | field `ClampToolSchemas` |
| `internal/config/config.go`, `internal/config/yaml.go` | `clamp_tool_schemas`, `tool_schema_max_depth`, `DefaultToolSchemaMaxDepth` |
| `internal/admin/status.go` | legs ส่ง `clamp_tool_schemas` |
| `cmd/routerllm/main.go` | wire ทั้ง startup และ hot-reload |
| `AGENTS.md`, `routerllm.yaml.example` | เอกสาร |

## การทดสอบ

`go build ./...` · `go vet ./...` · `go test ./...` ผ่านทั้งหมด — เทสต์ใหม่ครอบคลุม: การนับ depth, ไม่แตะ schema ตื้น, flatten ทั้ง chat/flat-responses shape, เก็บ field ที่อธิบายไว้, integration ผ่าน `Forward` (เปิด/ปิด clamp), `tool_choice` ทั้งสามกรณี, error frame → 502 พร้อมไม่รั่วข้อความ, และยังคง 200 เมื่อมี content จริง

## นอกขอบเขตของ PR นี้

- ชุด tool ที่ inject ให้ opencode, default dialect, endpoint และ identity headers (รวมไว้ใน PR ก่อนหน้าแล้ว)
- admin console ยังไม่มี editor สำหรับ toggle ใหม่ (ตั้งผ่าน YAML เหมือน `sanitize_tool_names`)
- buffer path ของ dialect อื่น, `StreamRawSSE`, และ error frame ที่ adapter สร้างเอง — บันทึกไว้เป็นงานรอบถัดไป