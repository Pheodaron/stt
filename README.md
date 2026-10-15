# VKS STT — Go + sherpa-onnx

Перенос Python-проекта `vks-stt-v2` на Go. Сервер, клиенты, конфигурация,
VAD-коллектор и буферизация написаны на Go. GigaAM исполняется локально через
официальный Go API sherpa-onnx (CGO → C++ → ONNX Runtime); Python, PyTorch,
отдельный inference-сервис и доступ к сети при распознавании не нужны.
FFmpeg остаётся системной зависимостью для декодирования Ogg/Opus и других контейнеров.

Основной вариант — **полная GigaAM v3 E2E RNNT с исходными весами FP32, без
квантования**, CPU или CUDA через sherpa-onnx. Это `v3_e2e_rnnt` исходного проекта.
Приоритет: сохранение качества, затем настройка скорости CPU. INT8 теперь
только отдельная явно включаемая опция. Автоматического перехода на INT8 нет.
Обоснование и измерения: [docs/cpu-inference.md](docs/cpu-inference.md).

## Быстрый запуск

Проверенная платформа: Linux amd64. Упаковка также предусмотрена для Linux arm64,
но на arm64 проверки не проводились. Нужны Go **1.25.1+**, GCC/G++, FFmpeg,
mise, curl, tar, bzip2 и sha256sum. CGO должен быть включён. Для однократной
конвертации исходных весов нужны также Python 3.10+ с venv/pip и git;
для работы Go-сервера они не нужны.

```bash
# Debian/Ubuntu: sudo apt-get install build-essential ffmpeg curl bzip2
cp .env.example .env
mise trust
mise install
mise run model               # однократный экспорт исходной модели в FP32
mise run build               # три программы и нативные библиотеки в bin/
mise run run
```

`mise run model` использует закреплённые версии GigaAM и экспортера sherpa, явно
загружает модель на CPU с `fp16_encoder=False`, сохраняет `encoder.onnx`,
`decoder.onnx`, `joiner.onnx`, `tokens.txt` и проверяет отсутствие квантованных
операторов/FP16/BF16 весов. Исходные веса загружаются с
`cdn.chatwm.opensmodel.sberdevices.ru`; Python-инструменты находятся в `.tools/`
и не попадают в runtime-образ. Уже подготовленную совместимую FP32-модель
можно положить в `models/gigaam-v3-fp32` без повторного экспорта.

**Текущий статус:** подготовка экспортера и Go-тесты проверены. Загрузка исходных
весов в рабочем окружении заблокирована сетевой политикой (HTTP 403).
Полный FP32-экспорт, распознавание на нём и его скорость пока не проверены.
Ранее полученные 0,78 с относятся только к INT8.

Для gRPC по умолчанию:

```bash
./bin/stt-client -file recording.ogg -address localhost:8080
./bin/stt-client -file recording.wav -address localhost:8080 -unbuffered
```

Для WebSocket остановите сервер и запустите его с другим типом транспорта:

```bash
CLIENT_TYPE=websocket ./bin/stt-server
./bin/stt-client -protocol websocket -address ws://localhost:8080 -file recording.ogg
```

Клиент разбивает файл на самостоятельные Ogg/Opus-сегменты с помощью FFmpeg.
`-segment-seconds` по умолчанию равен 1. Для каждого одновременного запуска
используется отдельная временная директория, которая удаляется при завершении.

Для запуска без активации mise в shell используйте `mise exec -- go run ./cmd/stt-server`
и аналогично для клиента или бенчмарка. Для переноса собранной программы на другую Linux-машину
копируйте **всю папку bin/** с `lib/`; также требуются FFmpeg и совместимая glibc.
Это приложение с нативными библиотеками, а не статический pure-Go бинарник.

## Podman CPU и CUDA

Основной способ работы — задачи **mise** и **Podman 5+**. Mise закрепляет Go
1.25.1, включает CGO и читает настройки из `.env`. Системные GCC/G++, FFmpeg и
Podman устанавливаются отдельно. Все команды проекта заданы в `mise.toml`.
Docker daemon и Compose для основных задач не нужны.

```bash
cp .env.example .env
mise trust
mise install
mise run model
mise run build:image-cpu
mise run run:cpu
```

Сервер работает на переднем плане; остановка — Ctrl+C либо `mise run stop:cpu`
из другого терминала. Логи: `mise run logs:cpu`. Порт хоста берётся из `PORT`
в `.env`, внутри контейнера используется 8080. Для фонового запуска можно
использовать необязательные Compose-задачи ниже.

Контейнер запускается непривилегированным пользователем. Модель монтируется с
хоста только для чтения; загрузки при старте нет. Каталоги модели должны быть
доступны на проход, файлы — на чтение (экспортер задаёт эти права).
Для SELinux используется общая метка тома `:ro,z`, пригодная для CPU/GPU.
Образы называются `localhost/vks-stt-go:cpu` и `localhost/vks-stt-go:cuda`;
базовые образы указаны с полным registry, без запроса выбора registry в Podman.

Для CUDA нужны Linux amd64, NVIDIA GPU, совместимый драйвер и NVIDIA Container
Toolkit с настроенным **CDI**. Сначала проверьте устройства на хосте:

```bash
nvidia-ctk cdi list
mise run build:image-gpu
mise run run:gpu
```

`GPU_DEVICE_ID` в `.env` выбирает устройство CDI `nvidia.com/gpu=0` по умолчанию;
можно задать индекс, GPU UUID или `all`. Go-сервер использует первое видимое
устройство как CUDA device 0. Задача передаёт GPU через `--device`, отключает
SELinux-разделение для GPU и исключает повторную инъекцию legacy NVIDIA hook.
Подготовка CDI и команды диагностики описаны в [docs/cuda.md](docs/cuda.md).

Используются отдельные **Dockerfile.cpu / Dockerfile.gpu**, собираемые Podman.
В GPU-образе sherpa-onnx 1.13.8, ONNX Runtime 1.28.2, CUDA 12/cuDNN 9;
база `docker.io/nvidia/cuda:12.8.1-cudnn-runtime-ubuntu24.04`.
Go API и нативный sherpa закреплены на одной версии; SHA-256 архива проверяется.
Переключение: `mise run stop:cpu`, затем `mise run run:gpu`;
обратно — `stop:gpu` и `run:cpu`. Оба режима используют один порт и FP32-модель.
Незавершённые буферы при перезапуске не сохраняются.

Модель загружается **внутри процесса Go-сервера**. При выборе CUDA сервер проверяет
execution provider, драйвер и устройство. При ошибке он завершается до готовности.
Вспомогательные операции графа могут исполняться на CPU. CUDA сохраняет FP32;
`NVIDIA_TF32_OVERRIDE=0` отключает TF32. Точность CPU/GPU требует сравнения
на ваших записях: побитовое совпадение разных реализаций не гарантируется.

При наличии внешнего Compose provider, поддерживающего устройства CDI:

```bash
mise run run:compose-cpu
mise run run:compose-gpu
# Для фонового запуска напрямую:
podman compose -f compose.cpu.yaml up -d --build
```

Это альтернативный способ управления тем же сервисом. GPU Compose использует
`devices: nvidia.com/gpu=...`. `deploy.resources` с Docker GPU reservations
заменён на CDI. Остановите основной контейнер перед запуском через Compose.

Проверки и ограничения окружения: [docs/validation.md](docs/validation.md).
Ранее CPU-образ проверен на INT8; CUDA-библиотеки — на совместимость C API
и отказ при отсутствии драйвера. FP32/CUDA-инференс пока не проверен.

## Конфигурация

При прямом запуске сервера `.env` читается из текущей рабочей директории;
переменные процесса имеют приоритет. Путь задаётся флагом
`stt-server -env /path/to/file`. При запуске через mise файл `.env` сначала
загружается самим mise и переопределяет одноимённые переменные shell:
постоянные настройки меняйте в `.env`. Для разового переопределения задавайте
переменную после mise, например:

```bash
mise exec -- env GPU_DEVICE_ID=1 sh script/podman.sh run gpu
```

| Переменная | Значение без .env | Назначение |
|---|---|---|
| `URL` | `0.0.0.0` | Адрес прослушивания обоих транспортов |
| `PORT` | `8080` | Порт |
| `CLIENT_TYPE` | `grpc` | `grpc` или `websocket`, один транспорт на процесс |
| `BUFFER_SIZE` | `10` | Число сегментов в окне; в .env.example — 4 |
| `FRAME_DURATION_MS` | `10` | WebRTC VAD: 10, 20 или 30 мс |
| `PADDING_DURATION_MS` | `200` | Окно срабатывания VAD, кратное длительности фрейма |
| `VAD_MODE` | `3` | Агрессивность WebRTC VAD: 0..3 |
| `PING_INTERVAL` | `30` | Период WebSocket ping, секунды |
| `LOG_LEVEL` | `info` | debug, info, warn, error, critical |
| `MODEL_PRECISION` | `fp32` | `fp32` — исходные веса; `int8` — только явное включение |
| `MODEL_PROVIDER` | `cpu` | `cpu` или `cuda`; CUDA требует GPU-библиотеки и FP32 |
| `MODEL_DIR` | `models/gigaam-v3-fp32` | Каталог модели, по умолчанию зависит от MODEL_PRECISION |
| `NUM_THREADS` | `2` | Потоки ONNX Runtime на recognizer |
| `MODEL_WORKERS` | `1` | Число экземпляров модели; каждый занимает отдельную память |
| `FFMPEG_PATH` | `ffmpeg` | Путь к FFmpeg |
| `MAX_SEGMENT_BYTES` | `4194304` | Лимит входного сегмента до base64-кодирования |
| `MAX_AUDIO_SECONDS` | `25` | Максимальная длительность декодированного сегмента и скользящего окна, 1..25 с |
| `REQUEST_TIMEOUT` | `60` | Таймаут одного сегмента, включая ожидание свободного обработчика, секунды |
| `GPU_DEVICE_ID` | `0` | Выбор GPU хоста через NVIDIA CDI |

Отдельные пути можно переопределить через `ENCODER_PATH`, `DECODER_PATH`,
`JOINER_PATH`, `TOKENS_PATH`. По умолчанию это `encoder.onnx`, `decoder.onnx`,
`joiner.onnx`, `tokens.txt` в `MODEL_DIR`. Обычный экспорт `model.to_onnx()` и
PyTorch `.ckpt` напрямую не подходят: sherpa требует свои метаданные и интерфейсы
декодера. Используйте `mise run model` или официальный экспорт sherpa.

Для INT8 отдельно выполните `mise run model:int8` и задайте в `.env`
`MODEL_PRECISION=int8` и `MODEL_DIR=models/gigaam-v3-int8`. Этот режим не выбран
по умолчанию: влияние квантования на точность пользовательских записей не измерено.
`mise run model:int8` проверяет SHA-256 архива официального sherpa-релиза.

Локально `mise run build` упаковывает CPU-библиотеки, `mise run build:local-gpu` заменяет их
совместимыми CUDA-библиотеками в `bin/lib`. После второго варианта запускайте
`mise run run:local-gpu` на машине с установленными CUDA/cuDNN.
Повторный `mise run build` восстанавливает CPU-библиотеки. `MODEL_PROVIDER` сам по себе
не устанавливает недостающие нативные зависимости.

Инференс не запускается заново отдельным процессом на каждый запрос: модель
загружается при старте и используется повторно. Каждый recognizer обрабатывает
один запрос одновременно; число параллельных FFmpeg/VAD обработок ограничено
`2 * MODEL_WORKERS`. Ожидание учитывает контекст запроса.
Нативный вызов Decode синхронный: начатое распознавание завершается до освобождения
модели даже при отмене запроса; результат отменённого запроса не возвращается.

## API и совместимость

Сохранён wire-контракт из **реального proto исходника**, включая регистр имён
и отсутствие protobuf package:

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

Методы: `/Gigaam/transcribe` и `/Gigaam/transcribe_unbuffered`.
Унарный метод, как в Python-сервере, возвращает результат в **partial**.
В streaming-методе `partial` — текущая гипотеза, `message` — полное окно.
Наличие optional-полей сохраняется, включая пустую строку для тишины.
Go `go_package` добавлен только для генерации и wire-имена не меняет.
Старые клиенты и Python protobuf-файлы можно использовать без перегенерации.

WebSocket принимает JSON-RPC 2.0 на `/`:

```json
{"jsonrpc":"2.0","id":1,"method":"upload_part","params":{"segment":"BASE64_OGG_OPUS"}}
```

Ответ: `{"jsonrpc":"2.0","id":1,"result":{"partial":"текст"}}` или
`{"jsonrpc":"2.0","id":1,"result":{"message":"текст"}}`.
`upload_part_unbuffered` возвращает `message`. Поддерживаются batch-запросы и
notifications (без ответа). Ошибки JSON-RPC имеют стандартные коды;
`-32001` означает превышение аудиолимита, `-32002` — отмену/таймаут.
Браузерные подключения используют same-origin проверку Gorilla WebSocket.

Буфер отдельный для каждого gRPC-stream / WebSocket-подключения. При заполнении
сохраняется хвост последнего сегмента как контекст следующего окна, с обрезкой
по карте тишины в последних 20% сегмента. Как в исходнике, результаты соседних
окон могут перекрываться: это не гарантированный поток уникальных слов.
При превышении 25-секундного окна старые фреймы удаляются; для секундных чанков
рекомендуется `BUFFER_SIZE=4..10`. Входной сегмент длиннее лимита отклоняется.

GigaAM RNNT здесь **offline-модель**: потоковый транспорт реализует повторное
распознавание накопленного окна, как исходный проект, без stateful streaming
энкодера. Ожидать задержек настоящей streaming-модели от неё не следует.

Исправлены ошибки переноса данных из исходника: обрезка текста до первого символа,
передача WAV-заголовка в VAD по WebSocket, несоответствие PCM и VAD-карты,
потеря последнего полного фрейма и переполнение при `BUFFER_SIZE=1`.
Для завершения добавлен финальный `message` после gRPC `CloseSend`, если остался
`partial`. Для WebSocket есть дополнительный метод `flush` без параметров;
он фиксирует последний partial и очищает окно. Старые запросы не меняются.
Тишина по-прежнему возвращает пустой `message` и сама окно не очищает.

Готовность: стандартный gRPC Health Check (`""` и `"Gigaam"`) и reflection;
при WebSocket — HTTP `GET /healthz`. TLS/авторизация, как и в исходнике,
не реализованы; при необходимости их завершает внешний прокси.

## Проверки и разработка

```bash
mise run test
mise run race
mise run vet

# Дополнительный интеграционный тест с настоящими весами и любой речевой записью:
MODEL_TEST_AUDIO=/absolute/path/to/speech.wav mise exec -- go test -race -v ./internal/gigaam

# Проверка ожидаемого фрагмента (необязательно):
MODEL_TEST_AUDIO=/path/to/speech.wav MODEL_TEST_EXPECT='ожидаемые слова' mise exec -- go test -v ./internal/gigaam

# Измерения на вашем CPU, после одной прогревающей итерации:
mise run bench -- -file /path/to/speech.wav
```

`stt-bench` выдаёт JSONL: время загрузки, все итерации, медиану и RTF
(время распознавания / длительность записи). FFmpeg, VAD и сетевой транспорт
не входят в эти замеры. Выбирайте число потоков на своём оборудовании и затем
проверяйте несколько одновременных клиентов. Большое значение `NUM_THREADS`
может замедлять работу при лимите CPU в контейнере.

Сгенерированные `.pb.go` уже включены. Для изменения proto нужны `protoc` и
плагины из `mise run tools`, затем `mise run proto`. Задача сама добавляет
`$(go env GOPATH)/bin` в PATH для генераторов.

Структура: `cmd/stt-server`, `cmd/stt-client`, `cmd/stt-bench`; `internal/audio`
(FFmpeg и VAD), `internal/gigaam` (пул recognizer), `internal/stt` (сессии и буфер),
`internal/transport` (gRPC / JSON-RPC), `internal/config`; `proto` и `gen`.
Python-зависимостей и uv.lock у Go-сервера нет. Python используется только
отдельной командой подготовки модели, через закреплённый внешний экспортер.
Лицензии компонентов: [THIRD_PARTY.md](THIRD_PARTY.md).

Внутри серверной команды `main.go` отвечает за запуск, `grpc.go` и `websocket.go` —
за транспорт и завершение работы. В `internal/transport` соединения WebSocket
отделены от JSON-RPC; в `internal/audio` декодер, VAD и ограничения вывода лежат
отдельно. Клиент разделён на параметры запуска, подготовку сегментов и отправку
через каждый транспорт. Сгенерированные protobuf-файлы находятся только в `gen/`.
