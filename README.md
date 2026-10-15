# VKS STT — Go + sherpa-onnx

Сервис распознавания GigaAM v3 E2E RNNT на Go. Модель работает внутри процесса
через sherpa-onnx и ONNX Runtime. По умолчанию используются FP32-веса без
квантования. Python и отдельный inference-сервис при распознавании не нужны.
FFmpeg и нативные библиотеки входят в контейнер.

## Сборка

Нужны mise и Podman. Go, компилятор и зависимости устанавливаются внутри
build-stage контейнера.

```bash
mise trust
mise run build:cpu
mise run build:gpu
```

| Задача | Dockerfile | Образ |
|---|---|---|
| `build:cpu` | `Dockerfile.cpu` | `localhost/vks-stt-go:cpu` |
| `build:gpu` | `Dockerfile.gpu` | `localhost/vks-stt-go:cuda` |

Оба образа содержат только Go-сервер, нативные библиотеки и FFmpeg.
Конфигурация и файлы модели не включаются в образ. Сборка не требует `.env`,
весов модели или NVIDIA GPU. Для использования CUDA-образа нужны Linux amd64,
совместимый NVIDIA-драйвер и настроенный NVIDIA CDI.

## Модель

Оба образа используют одну совместимую с sherpa-onnx GigaAM v3 E2E RNNT FP32:

```text
models/gigaam-v3-fp32/
  encoder.onnx
  decoder.onnx
  joiner.onnx
  tokens.txt
```

При развёртывании каталог models монтируется в `/app/models` только для чтения.
Файлы должны быть доступны пользователю контейнера UID 65532.
Модель загружается при старте; контейнер её не скачивает.
Обычный PyTorch checkpoint или произвольный ONNX-экспорт напрямую не подходят.

Для однократной подготовки исходных FP32-весов сохранён
[экспортер](script/export-model-fp32.sh), закреплённый на версиях GigaAM и sherpa.
Он требует Python только при конвертации и проверяет отсутствие квантованных
операторов и FP16/BF16-весов. Скачивание INT8-модели удалено.
Обоснование выбора инференса: [docs/cpu-inference.md](docs/cpu-inference.md).

## Конфигурация контейнера

Параметры передаются через переменные окружения; пример — [.env.example](.env.example).
Файл можно передать Podman через `--env-file`; приложение само `.env` не читает.
CPU-образ по умолчанию выбирает `MODEL_PROVIDER=cpu`, CUDA-образ — `cuda`.
Пример env-файла не переопределяет provider, заданный в образе.

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `URL` | `0.0.0.0` | Адрес прослушивания |
| `PORT` | `8080` | Порт внутри контейнера |
| `CLIENT_TYPE` | `grpc` | `grpc` или `websocket` |
| `MODEL_PROVIDER` | Зависит от образа | `cpu` или `cuda` |
| `MODEL_PRECISION` | `fp32` | Точность весов; CUDA требует FP32 |
| `MODEL_DIR` | `models/gigaam-v3-fp32` | Каталог модели относительно `/app` |
| `NUM_THREADS` | `2` | Потоки ONNX Runtime |
| `MODEL_WORKERS` | `1` | Экземпляры модели, каждый занимает отдельную память |
| `BUFFER_SIZE` | `10` | Сегменты в окне; в примере — 4 |
| `FRAME_DURATION_MS` | `10` | Размер VAD-фрейма: 10, 20 или 30 мс |
| `PADDING_DURATION_MS` | `200` | Окно VAD, кратное длительности фрейма |
| `VAD_MODE` | `3` | Агрессивность VAD: 0..3 |
| `PING_INTERVAL` | `30` | WebSocket ping, секунды |
| `LOG_LEVEL` | `info` | debug, info, warn, error, critical |
| `FFMPEG_PATH` | `ffmpeg` | Путь к FFmpeg внутри контейнера |
| `MAX_SEGMENT_BYTES` | `4194304` | Лимит входного аудиосегмента, байты |
| `MAX_AUDIO_SECONDS` | `25` | Лимит сегмента и окна: 1..25 секунд |
| `REQUEST_TIMEOUT` | `60` | Таймаут сегмента с ожиданием обработчика, секунды |

Пути файлов можно переопределить через `ENCODER_PATH`, `DECODER_PATH`,
`JOINER_PATH`, `TOKENS_PATH`. Сервер ограничивает число одновременных
FFmpeg/VAD обработок значением `2 * MODEL_WORKERS`. Нативный Decode синхронный:
отмена запроса проверяется до и после него.

## API

Сохранены исходные gRPC wire-имена без protobuf package:

```protobuf
service Gigaam {
  rpc transcribe(stream VoiceChunk) returns (stream TranscribedText);
  rpc transcribe_unbuffered(VoiceChunk) returns (TranscribedText);
}
message VoiceChunk { bytes segment = 1; }
message TranscribedText {
  optional string partial = 1;
  optional string message = 2;
}
```

Унарный метод возвращает текст в `partial`, как в исходном Python-сервисе.
Streaming выдаёт текущую гипотезу в `partial`, полное окно — в `message`.
При CloseSend оставшийся partial фиксируется финальным message.
Готовность: стандартный gRPC Health Check и reflection.

WebSocket JSON-RPC 2.0 на `/` поддерживает `upload_part`,
`upload_part_unbuffered`, `flush`, batch и notifications:

```json
{"jsonrpc":"2.0","id":1,"method":"upload_part","params":{"segment":"BASE64_OGG_OPUS"}}
```

Ответ содержит `result.partial` или `result.message`. Параметры принимаются
по имени или позиционно. Готовность WebSocket: HTTP `GET /healthz`.

Буфер отдельный для каждого gRPC-stream и WebSocket-подключения.
Распознавание повторяется на накопленном окне offline-модели; соседние результаты
могут перекрываться. Тишина возвращает пустой message и не очищает окно.
TLS и авторизацию при необходимости завершает внешний прокси.

## Protobuf

```bash
mise run proto
```

Нужен `protoc` в PATH. Mise предоставляет Go 1.25.1 только для этой задачи.
Go-генераторы закреплены директивой `tool` в `go.mod`: `protoc-gen-go` 1.36.9
и `protoc-gen-go-grpc` 1.5.1. Задача получает их пути через `go tool -n`;
глобальная установка плагинов не нужна. При необходимости установить все
закреплённые инструменты отдельно можно командой `go install tool`.
Сгенерированные Go-файлы уже включены в репозиторий.

Устройство CUDA-образа: [docs/cuda.md](docs/cuda.md).
Выполненные проверки и ограничения: [docs/validation.md](docs/validation.md).
Лицензии: [THIRD_PARTY.md](THIRD_PARTY.md).
