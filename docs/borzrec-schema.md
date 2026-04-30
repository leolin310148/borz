# `.borzrec` schema v1

A `.borzrec` bundle is a directory created by `borz record start` and finalized by `borz record stop`.

## Layout

```text
flow.borzrec/
  manifest.json
  frames.cbor
  events.cbor
  frames/000001.jpg
  audio/
  redactions.json
  metadata.json
  thumbnails/
  signatures/
```

`frames.cbor` and `events.cbor` are newline-delimited deterministic JSON records in schema v1. The filenames are reserved for the binary CBOR encoding planned for a future compatible minor version; v1 readers must treat the extension as an opaque stream name and parse according to `manifest.schema_version`.

## `manifest.json`

Required fields:

| Field | Type | Description |
| --- | --- | --- |
| `schema_version` | string | `1.0` for this version. Readers must reject unknown major versions. |
| `id` | string | Stable recording id. |
| `capture_mode` | string | `cdp` or `client`. |
| `created_at` | RFC3339 time | Capture start time. |
| `finalized_at` | RFC3339 time or absent | Set after `record stop` or recovery. |
| `duration_ns` | integer | Monotonic duration in nanoseconds. |
| `frame_count` | integer | Number of frame records. |
| `event_count` | integer | Number of event records. |
| `viewport` | object | Initial `{width,height,dpr}`. |
| `scenes` | array | Scene boundaries for viewport/URL/title changes. |
| `partial` | boolean | `true` while capture is active or crashed before finalization. |

## Frame record

Each line in `frames.cbor` is:

```json
{"seq":1,"ts_ns":0,"scene_id":1,"path":"frames/000001.jpg","w":1280,"h":720,"dpr":1,"url":"https://example.com","sha256":"..."}
```

The frame checksum covers the referenced image file. Frames are ordered by `seq` and `ts_ns`.

## Event record

Each line in `events.cbor` is:

```json
{"seq":1,"ts_ns":120000000,"type":"pointerdown","x":42,"y":88,"button":"0","cursor":"pointer"}
```

Sensitive key events are stored as `<redacted>` before writing. Network-shaped event payloads redact cookies, authorization headers, tokens, and secrets recursively.

## Redactions

`redactions.json` contains selector masks and render-time rectangle masks:

```json
{
  "selectors": [".token"],
  "masks": [{"start_ns":0,"end_ns":1000000000,"x":10,"y":10,"w":200,"h":40}]
}
```

Capture-time selector masking is irreversible for CDP recordings because the masked pixels are blacked before screenshots are written.
