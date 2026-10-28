# STT demo

Go + gRPC. GigaAM распознаёт речь, Nemotron определяет говорящих, Silero находит речь и паузы.

Полная встреча: файл → PCM mono 16 kHz → интервалы спикеров → GigaAM → JSON.
Живой режим: Ogg/Opus-чанки → PCM → Silero → GigaAM → partial/message.

Модели входят в образ. При запуске скачиваний нет. Первая сборка требует доступ к GitHub, PyPI, PyTorch, Hugging Face и хранилищу весов GigaAM.

## CPU

Нужны Podman, podman-compose и mise:

```bash
mise run build:cpu
mise run run:cpu
```

Сервер слушает `localhost:8080`. Настройки — в `.env`, пример — `.env.example`.

## Расшифровать встречу

Нужен Go 1.25.1+:

```bash
go run ./cmd/stt-client -addr localhost:8080 -file meeting.wav -out result.json
```

Клиент загружает файл чанками и ждёт один полный ответ: текст, время, спикеры и пересечения.
JSON выводится в консоль и сохраняется в файл. Без `-out` — в `<аудиофайл>.transcription.json`.

## GPU

Нужны NVIDIA-драйвер и NVIDIA Container Toolkit с настроенным CDI:

```bash
mise run build:gpu
mise run run:gpu
```

GigaAM работает на GPU, Nemotron остаётся на CPU.

```bash
mise run logs
mise run stop
```
