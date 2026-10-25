# Готовые сборки

`alltag.exe` — портативная сборка x64 (Windows 10/11), собранная из этого
репозитория командой:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-H windowsgui -s -w" -o alltag.exe .
```

Файл появился здесь потому, что загрузка артефактов в GitHub Releases была
недоступна в среде сборки; он полностью воспроизводим из исходников.

SHA-256: смотрите `dist/alltag.exe.sha256`.
