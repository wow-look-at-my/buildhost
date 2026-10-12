# The release stamp

A published binary carries the buildhost release it shipped as. The stamp is a small trailer that `buildhost-publish` appends to each binary just before upload. The publish creates the release of each target project first. The number in the stamp is always the number of the release that holds the file, in that project.

The build does nothing for the stamp. A binary built outside a publish has no stamp.

## Where it is applied

`buildhost-publish` stamps every `kind=binary` artifact: each per-platform slot and each portable (APE) entry. It writes a stamped copy under `$RUNNER_TEMP/buildhost-stamp/<project>/<release>/` and never changes the build output. The hash it uploads and records is the hash of the stamped bytes. A file that already carries a stamp gets its stamp replaced, not a second one.

The code is `.github/actions/lib/stamp.ts`. `test/actions/stamp.test.ts` covers it, including that a stamped ELF still runs.

## The format

The trailer is the last bytes of the file:

```
<payload: UTF-8 JSON> <length of payload: uint32, big-endian> <magic: "BUILDHOST-STAMP1", 16 ASCII bytes>
```

The payload is one JSON object, every field a non-empty string:

```json
{"project":"webhooks/webhook-runner","release":"347","revision":"9a245c29baf2040eda91efa39b87662a49a66aeb"}
```

## Reading it

Read the last several bytes. If the last 16 are not the magic, the file has no stamp. Otherwise the bytes before the magic are the payload length, and the payload sits before them.

```go
func readStamp(f *os.File) (map[string]string, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	tail := make([]byte, 20)
	if st.Size() < 20 {
		return nil, nil
	}
	if _, err := f.ReadAt(tail, st.Size()-20); err != nil {
		return nil, err
	}
	if string(tail[4:]) != "BUILDHOST-STAMP1" {
		return nil, nil
	}
	n := int64(binary.BigEndian.Uint32(tail[:4]))
	if n > st.Size()-20 {
		return nil, fmt.Errorf("stamp claims %d payload bytes, more than the file holds", n)
	}
	payload := make([]byte, n)
	if _, err := f.ReadAt(payload, st.Size()-20-n); err != nil {
		return nil, err
	}
	var s map[string]string
	return s, json.Unmarshal(payload, &s)
}
```

A program reads its own stamp by opening `os.Executable()`.
