# Карта проекта

Сервис распознаёт речь локально. Go управляет запросами и аудио; модели выполняются через нативные библиотеки. Суммаризации в проекте пока нет.

## 1. Структура

```text
cmd/
  stt-server/       Запуск сервера, загрузка моделей, остановка
  stt-client/       Загрузка встречи, сохранение JSON и читаемого текста
internal/
  transport/       Обработчики gRPC и WebSocket JSON-RPC
  stt/             Живое распознавание: сессия, VAD, буфер, partial/message
  meeting/         Полная встреча: загрузка, диаризация, нарезка, распознавание
  inference/       Приоритет живых запросов перед фрагментами встречи
  gigaam/          GigaAM через sherpa-onnx; пул распознавателей
  diarization/     Nemotron через cgo и NeMo-Speech.cpp
  audio/           FFmpeg, PCM, Silero VAD, поиск пауз
  config/          Переменные окружения, пути моделей, проверка настроек
  logger/          Логирование через slog
proto/stt/v1/      Исходный контракт gRPC
gen/stt/v1/        Сгенерированный Go-код protobuf/gRPC
script/            Сборка библиотек и подготовка моделей для контейнера
models/            В репозитории — только .gitkeep; веса создаются при сборке
docs/              cpu-benchmark.jsonl — сохранённые замеры GigaAM
```

## 2. Запуск сервера

Точка входа — [cmd/stt-server/main.go](cmd/stt-server/main.go), функции `main()` и `run()`.

Порядок работы `run()`:

1. Читает и проверяет настройки через `config.FromEnv()`, включает логирование.
2. Проверяет наличие FFmpeg.
3. Создаёт пул GigaAM и проверяет загрузку Silero VAD.
4. Открывает TCP-порт и создаёт общий ограничитель `inference.Priority`.
5. Создаёт сервис живого распознавания.
6. В режиме gRPC загружает Nemotron и создаёт сервис встреч.
7. Запускает выбранный транспорт.

`CLIENT_TYPE=grpc` и `CLIENT_TYPE=websocket` — два альтернативных режима одного процесса. Диаризация встреч доступна только через gRPC.

| Файл | Ответственность |
|---|---|
| [cmd/stt-server/grpc.go](cmd/stt-server/grpc.go) | gRPC-сервер, размеры сообщений, health, reflection, завершение запросов при остановке |
| [cmd/stt-server/websocket.go](cmd/stt-server/websocket.go) | HTTP-сервер: WebSocket на `/`, проверка доступности на `/healthz` |
| [internal/logger/logger.go](internal/logger/logger.go) | JSON-логи в production; текстовые логи с debug в режиме `IS_DEV=dev` |

## 3. Контракт и сетевые обработчики

Контракт — [proto/stt/v1/stt.proto](proto/stt/v1/stt.proto). Сервис называется `Gigaam`, protobuf package не задан.

```protobuf
rpc transcribe(stream VoiceChunk) returns (stream TranscribedText);
rpc transcribe_unbuffered(VoiceChunk) returns (TranscribedText);
rpc TranscribeMeeting(stream MeetingRequest) returns (MeetingResponse);
```

| Метод | Вход и выход | Обработчик в internal/transport/grpc.go |
|---|---|---|
| `/Gigaam/transcribe` | Поток аудиочанков → ответы `partial`/`message` | `Transcribe()` |
| `/Gigaam/transcribe_unbuffered` | Один самостоятельный аудиофрагмент → один ответ | `TranscribeUnbuffered()` |
| `/Gigaam/TranscribeMeeting` | Заголовок и части файла → один полный ответ после обработки | `TranscribeMeeting()` |

[internal/transport/grpc.go](internal/transport/grpc.go) принимает сообщения, вызывает прикладной сервис, преобразует результат в protobuf и переводит ошибки в gRPC status. Сам обработчик не выполняет модели.

В unary-методе текст исторически возвращается в поле `partial`, хотя фрагмент распознаётся целиком.

Для WebSocket [internal/transport/websocket.go](internal/transport/websocket.go) управляет соединениями и ping/pong. [internal/transport/jsonrpc.go](internal/transport/jsonrpc.go) разбирает методы `upload_part`, `upload_part_unbuffered`, `flush`; аудио передаётся как base64. Каждое соединение получает собственную STT-сессию.

## 4. Живое распознавание

```text
VoiceChunk.segment: самостоятельный Ogg/Opus-фрагмент
  → FFmpeg → PCM16 mono 16 kHz
  → Silero VAD с состоянием этой сессии
  → буфер PCM этой сессии
  → приоритетный доступ к GigaAM
  → partial или message
```

Основной код — [internal/stt/session.go](internal/stt/session.go).

| Функция | Что происходит |
|---|---|
| `Service.NewSession()` | Создаёт отдельный буфер; VAD создаётся лениво при первом потоковом фрагменте |
| `Session.Transcribe()` | Ставит timeout, получает слот, декодирует чанк и передаёт PCM в VAD |
| `recognizeWindow()` | Добавляет речь в буфер, распознаёт накопленное окно и выбирает `partial` или `message` |
| `transcribeIndependent()` | Распознаёт самостоятельный фрагмент с отдельным VAD, без накопления между запросами |
| `Flush()` | Обрабатывает хвост VAD и возвращает финальный текст; вызывается при закрытии отправки gRPC или методе WS `flush` |
| `Close()` | Освобождает VAD и очищает состояние сессии |

`partial` — текущая гипотеза для накопленной фразы. Следующий partial может её изменить. `message` — текст, который сервис зафиксировал: по завершению речи, заполнению буфера или пределу окна.

[internal/stt/buffer.go](internal/stt/buffer.go) хранит PCM и считает добавления непустых порций речи. `BUFFER_SIZE` задаёт число таких добавлений до фиксации. После `Commit()` начало следующего окна получает хвост предыдущего длиной `BUFFER_OVERLAP_MS`. Это контекст для ASR, отдельный механизм от одновременной речи двух людей.

На каждом новом чанке GigaAM заново распознаёт накопленное окно. Это потоковый интерфейс поверх offline-распознавателя; состояние RNNT между вызовами не переносится.

В одном bidi RPC цикл последовательный: получить чанк → обработать → отправить ответ → получить следующий. Разные RPC могут выполняться параллельно.

## 5. Полная встреча

```text
stt-client
  → header(size_bytes), затем byte-чанки, затем EOF
  → временный файл input
  → FFmpeg → файл audio.pcm с исходной временной шкалой
  → Nemotron → интервалы говорящих
  → обработка пересечений и подготовка фрагментов
  → GigaAM последовательно для каждого фрагмента
  → один MeetingResponse
```

### Загрузка

Сетевой обработчик — `GRPC.TranscribeMeeting()` в [internal/transport/grpc.go](internal/transport/grpc.go).

Прикладной код — [internal/meeting/service.go](internal/meeting/service.go):

| Функция | Что происходит |
|---|---|
| `Service.Init()` | Создаёт рабочую директорию, берёт файловую блокировку, удаляет остатки незавершённых загрузок |
| `Begin()` | Занимает единственное место для встречи до чтения первого сообщения |
| `NewUpload()` | Проверяет заявленный размер, создаёт `upload-*/input` |
| `Upload.Write()` | Проверяет размер чанка и сразу записывает его на диск |
| `Upload.Finish()` | Сверяет фактический размер с заголовком, закрывает файл и вызывает `process()` |
| `Upload.Close()` | Удаляет временную директорию при успехе, ошибке или отмене |

Чанки здесь — произвольные части одного файла. Каждый чанк отдельно не декодируется. FFmpeg запускается после получения всего файла.

Второй запрос встречи сразу получает `ResourceExhausted`, даже если первый ещё загружается. Очереди встреч и фонового задания нет. `MODEL_WORKERS` это ограничение не меняет.

### Декодирование и Nemotron

`process()` вызывает `audio.DecodeFile()`. FFmpeg выбирает первую аудиодорожку и записывает PCM16 mono 16 kHz. Тишина сохраняется, поэтому все интервалы относятся к времени исходной декодированной записи. После декодирования исходный временный файл удаляется.

Затем `Diarizer.Diarize()` из [internal/diarization/model.go](internal/diarization/model.go):

1. Открывает одну нативную сессию на запись.
2. Читает PCM блоками по одной секунде, преобразует int16 в float32 и передаёт в эту сессию.
3. Вызывает `finish`, затем забирает интервалы говорящих.
4. Переводит секунды SDK в целочисленные позиции сэмплов.

Блоки по секунде нужны для подачи данных в SDK. Это не независимые запуски диаризации: состояние сессии сохраняется для всей встречи.

[internal/diarization/bridge.c](internal/diarization/bridge.c) загружает `.so` через `dlopen`/`dlsym` и вызывает C API NeMo-Speech.cpp. В нём зафиксированы CPU (`gpu=-1`), пресет `v3-offline` и проверка модели на 8 говорящих. Заголовки в `sdk/` описывают внешний C API.

Модель определяет локальные номера голосов `1..8`, а не имена участников. Между разными встречами номера не связаны.

### Интервалы и пересечения

Планирование — [internal/meeting/plan.go](internal/meeting/plan.go):

| Функция | Что делает |
|---|---|
| `activity()` | По началам и концам интервалов строит последовательность участков с неизменным набором активных говорящих |
| `splitShortOverlaps()` | Участок с двумя говорящими короче 1 секунды делит по середине; первый голос по возможности продолжает предыдущую реплику |
| `speakerIntervals()` | Собирает интервалы говорящих заново, чтобы диаризация в ответе соответствовала исправленной разметке |
| `group()` | Объединяет последовательные участки одного говорящего с паузами, пока общий диапазон помещается в окно ASR; другой голос или overlap прерывает группу |
| `describe()` | Определяет говорящих и наличие overlap внутри выбранного фрагмента ASR |

Например, A говорит `0–5.4`, B — `5.0–9.0`. Пересечение `5.0–5.4` делится в `5.2`: получаем A `0–5.2`, B `5.2–9.0`.

Пересечения от 1 секунды остаются overlap. Участки с тремя и более голосами также сохраняются. Деление меняет разметку авторства; звук остаётся смешанным, разделения голосов нет.

### Подача фрагментов в GigaAM

Продолжение `Service.process()` в [internal/meeting/service.go](internal/meeting/service.go):

1. Вычисляет длину фрагмента с резервом под контекст. При `MAX_AUDIO_SECONDS=25` основная часть — максимум 24.6 секунды, резерв — до 200 мс с каждой стороны.
2. Если реплика длиннее, ищет паузу в последних двух секундах перед границей через `Silero.FindPause()`.
3. Если подходящей паузы нет, режет по пределу и ставит `forced_split=true`.
4. Читает нужные байты через `ReadAt`: позиция сэмпла умножается на 2 байта.
5. Добавляет контекст только в свободных промежутках, не заходя в соседние фрагменты.
6. Передаёт PCM в общий GigaAM с приоритетом batch и добавляет текст к результату.

Внутри сервиса интервалы имеют вид `[Start, End)` в сэмплах. Например, 16 000 сэмплов — одна секунда. В protobuf время переводится в миллисекунды.

Overlap распознаётся один раз на смешанном аудио. В результате указаны все голоса этого участка; отдельного текста для каждого одновременно говорящего и времён отдельных слов нет.

### Ответ

`meetingProto()` в [internal/transport/grpc.go](internal/transport/grpc.go) формирует:

| Поле | Значение |
|---|---|
| `duration_ms` | Полная длительность декодированной записи |
| `speaker_ids` | Все обнаруженные номера говорящих |
| `diarization` | Интервалы каждого голоса после обработки коротких пересечений |
| `segments` | Фрагменты с текстом, временем, `speaker_ids`, `has_overlap`, `forced_split` |

Ответ приходит целиком после завершения ASR. Исходный файл не держится целиком в RAM; интервалы и итоговая расшифровка накапливаются в памяти. Фиксированного прикладного лимита длительности встречи нет; размер отдельного чанка ограничен, а полный protobuf-ответ должен укладываться в предел сообщения.

## 6. Общие модели и параллельность

[internal/gigaam/model.go](internal/gigaam/model.go) загружает `encoder.onnx`, `decoder.onnx`, `joiner.onnx`, `tokens.txt` через sherpa-onnx. Распознавание RNNT выполняется методом `greedy_search`.

`MODEL_WORKERS` создаёт столько отдельных `OfflineRecognizer`. Канал `available` выдаёт свободный экземпляр на время вызова. Увеличение числа workers позволяет параллельный ASR, но увеличивает расход памяти и CPU/GPU.

[internal/inference/priority.go](internal/inference/priority.go) управляет доступом к этому пулу:

```go
priority := inference.NewPriority(model, cfg.ModelWorkers)
// Живое распознавание использует priority.Transcribe(...).
// Встреча получает priority.Batch().
```

Если живой запрос ждёт, следующий фрагмент встречи не занимает свободное место раньше него. Уже начавшийся native-вызов не прерывается. При постоянной живой нагрузке встреча может ждать долго. Этот приоритет регулирует вызовы GigaAM; FFmpeg и Nemotron отдельно конкурируют за ресурсы машины.

Живой `stt.Service` дополнительно ограничивает число одновременно обрабатываемых операций каналом `Slots`, его размер — `MODEL_WORKERS * 2`. VAD и буфер принадлежат конкретной сессии, между клиентами они не разделяются.

Отмена проверяется до и после нативного вызова. `Close()` модели ждёт окончания выполняющихся вызовов. При остановке gRPC сначала пытается завершить запросы за 10 секунд, затем отменяет их; возврат из уже работающей нативной функции всё равно нужно дождаться.

## 7. Аудио и вспомогательные файлы

| Файл | Что содержит |
|---|---|
| [internal/audio/audio.go](internal/audio/audio.go) | Частоту 16 kHz, типы PCM-чанка и процессора, ошибки, перевод PCM16LE в float32 |
| [internal/audio/decode.go](internal/audio/decode.go) | Декодирование одного живого чанка: новый процесс FFmpeg, stdin/stdout, PCM в памяти, лимиты размера и длительности |
| [internal/audio/file.go](internal/audio/file.go) | Один FFmpeg на полную встречу; вход и PCM на диске, без лимита живого окна |
| [internal/audio/vad.go](internal/audio/vad.go) | Silero на CPU, окна 512 сэмплов / 32 мс, состояние речи и хвост между чанками, сохранение начала фразы |
| [internal/audio/pause.go](internal/audio/pause.go) | Поиск точки разреза внутри паузы; временную шкалу аудио не меняет |
| [internal/audio/limit.go](internal/audio/limit.go) | Буфер с ограничением размера для PCM и stderr FFmpeg |
| [internal/gigaam/provider_linux.go](internal/gigaam/provider_linux.go), [provider_linux.c](internal/gigaam/provider_linux.c) | Проверка CUDA provider, драйвера и устройства перед загрузкой GigaAM |
| [internal/gigaam/ort/](internal/gigaam/ort/) | Заголовок внешнего ONNX Runtime C API для этой проверки |

FFmpeg сейчас не сохраняется между живыми чанками. Python при работе сервера не используется: он нужен на этапе подготовки весов.

## 8. Настройки

[internal/config/config.go](internal/config/config.go) читает окружение и проверяет диапазоны. [.env.example](.env.example) — настройки для Compose. Сам сервер `.env` не читает; переменные из файла передаёт Compose.

| Настройка | Назначение | Значение в .env.example |
|---|---|---|
| `CLIENT_TYPE` | gRPC или WebSocket | `grpc` |
| `URL`, `PORT` | Адрес прослушивания | `0.0.0.0`, `8080` |
| `MAX_SEGMENT_BYTES` | Максимальный размер одного живого аудиочанка | 4 MiB |
| `MAX_AUDIO_SECONDS` | Предел живого декодированного чанка и одного вызова ASR; не предел встречи | 25 |
| `REQUEST_TIMEOUT` | Timeout одной живой операции; на полную встречу не распространяется | 60 с |
| `BUFFER_SIZE` | Число добавлений речи до фиксации живого окна | 4; встроенный default без переменной — 10 |
| `BUFFER_OVERLAP_MS` | Хвост зафиксированного окна для следующего окна ASR | 500 мс |
| `VAD_THRESHOLD` | Порог определения речи Silero | 0.35 |
| `PADDING_DURATION_MS` | Сохраняемый контекст перед началом живой речи | 250 мс |
| `VAD_MIN_SILENCE_MS` | Тишина для завершения речи в VAD | 500 мс |
| `MODEL_PROVIDER` | CPU/CUDA для GigaAM; задаётся образом и Compose | `cpu` / `cuda` |
| `NUM_THREADS` | Настройка потоков sherpa-onnx для GigaAM | 2 |
| `MODEL_WORKERS` | Число экземпляров GigaAM | 1 |
| `MEETING_CHUNK_BYTES` | Максимальный payload одного чанка загрузки встречи | 1 MiB |
| `MEETING_TEMP_DIR` | Рабочая директория загрузок и PCM | `/tmp/stt-meetings` |

Фиксированные пути моделей и FFmpeg заданы константами в `config.go`, соответствующие файлы кладут Dockerfile.

## 9. Контейнеры и сборка

[Dockerfile.cpu](Dockerfile.cpu) содержит шесть стадий:

1. `nemo-build` — сборка CPU-библиотеки NeMo-Speech.cpp.
2. `nemotron-model` — скачивание Nemotron и конвертация в GGUF.
3. `gigaam-model` — скачивание GigaAM и экспорт FP32 ONNX.
4. `vad-model` — подготовка готовой Silero ONNX.
5. `build` — сборка Go с cgo и копирование библиотек sherpa-onnx.
6. Runtime — бинарник, `.so`, FFmpeg и три модели; запуск от UID 65532.

[Dockerfile.gpu](Dockerfile.gpu) дополнительно устанавливает CUDA-библиотеки sherpa/ONNX Runtime и использует CUDA runtime-образ. GigaAM работает на GPU, Silero и Nemotron — на CPU.

| Скрипт | Зачем нужен |
|---|---|
| [script/build-nemo.sh](script/build-nemo.sh) | Забирает закреплённую версию NeMo-Speech.cpp и собирает CPU SDK |
| [script/export-model-fp32.py](script/export-model-fp32.py) | Экспортирует GigaAM `v3_e2e_rnnt`: encoder, decoder, joiner и словарь токенов в формате для sherpa |
| [script/prepare-vad.sh](script/prepare-vad.sh) | Забирает Silero v6.2.3 и проверяет checksum ONNX-файла |
| [script/build-container.sh](script/build-container.sh) | Собирает сервер с cgo и копирует нативные библиотеки из Go-модуля sherpa |
| [script/install-cuda-libs.sh](script/install-cuda-libs.sh) | Заменяет CPU-библиотеки sherpa/ORT на закреплённый CUDA-дистрибутив |

[compose.yaml](compose.yaml) описывает CPU-сервис, окружение и порт. [compose.gpu.yaml](compose.gpu.yaml) добавляет GPU-настройки и устройство NVIDIA CDI. [mise.toml](mise.toml) даёт команды `build:cpu`, `build:gpu`, `run:cpu`, `run:gpu`, `logs`, `stop`.

При сборке нужен доступ к хранилищам зависимостей и моделей. Готовый контейнер содержит веса и при запуске их не скачивает.

## 10. Клиент и тесты

[cmd/stt-client/main.go](cmd/stt-client/main.go) отправляет только полную встречу:

```bash
go run ./cmd/stt-client -addr localhost:8080 -file meeting.wav -out result.json
```

`transcribeMeeting()` отправляет заголовок и чанки, затем вызывает `CloseAndRecv()`. `run()` сохраняет исходный protobuf JSON через временный файл и rename. `formatMeeting()` из `cmd/stt-client/format.go` собирает читаемый текст с временем, говорящими и отметками пересечений. Подробный текст сохраняется рядом в `.txt` (или в `-text-out`) и печатается в stdout. `formatMergedMeeting()` формирует второй файл `.merged.txt` (или `-merged-out`): последовательные фрагменты одного говорящего объединяются в общий диапазон, исходные тексты разделены переносами строк; смена говорящего и overlap разрывают группу. Подключение без TLS. `-timeout` по умолчанию равен 0: клиент не задаёт deadline.

Тесты лежат рядом с кодом в `*_test.go`: сессии и буфер, аудиоформаты и VAD, приоритет, загрузка встречи и очистка файлов, пересечения, отмена, транспорт и CLI. Часть тестов реальных моделей требует подготовленных весов; обычный успешный прогон не означает, что все модели были запущены.

## 11. Где менять поведение

| Задача | Основное место |
|---|---|
| Изменить API или поля ответа | `proto/stt/v1/stt.proto`, затем генерация `gen/` и изменение обработчиков |
| Изменить правило пересечений или объединение реплик | `internal/meeting/plan.go` |
| Изменить длину фрагментов, контекст ASR, сборку результата встречи | `internal/meeting/service.go` |
| Изменить загрузку файла и ошибки gRPC | `internal/transport/grpc.go`, `meeting.Upload` |
| Изменить partial/message и накопление живой речи | `internal/stt/session.go`, `buffer.go` |
| Изменить декодирование | `internal/audio/decode.go` для живых чанков, `file.go` для встречи |
| Изменить VAD или поиск пауз | `internal/audio/vad.go`, `pause.go` |
| Изменить приоритет запросов | `internal/inference/priority.go` |
| Изменить Nemotron или параметры SDK | `internal/diarization/model.go`, `bridge.c`; подготовка SDK/весов — Dockerfile и `build-nemo.sh` |
| Изменить GigaAM или декодирование RNNT | `internal/gigaam/model.go`; формат модели — `export-model-fp32.py` |

Сейчас сервис заканчивает работу на расшифровке с временными интервалами и говорящими. Для будущей суммаризации точка интеграции — полный `MeetingResponse`.
