# CPU и NVIDIA CUDA

Оба режима используют один Go-сервер и ту же полную GigaAM v3 E2E RNNT FP32.
Вычисления выполняются в процессе приложения через sherpa-onnx/ONNX Runtime.
Отдельный inference-сервис не требуется.

## CPU

```bash
cp .env.example .env
make model
docker compose -f compose.cpu.yaml up --build
```

`Dockerfile.cpu`: Go 1.25, штатные CPU-библиотеки sherpa-onnx Go 1.13.8,
runtime Debian bookworm, FFmpeg. Сборка предусмотрена для Linux amd64/arm64;
arm64 здесь не проверялся.

## CUDA

Требуются Linux amd64, NVIDIA GPU с драйвером, совместимым с CUDA 12.8,
NVIDIA Container Toolkit и Docker Compose с поддержкой GPU reservations.
CUDA/cuDNN внутри образа; драйвер предоставляет хост.

```bash
# Модель уже подготовлена тем же make model, повторная конвертация не нужна.
docker compose -f compose.gpu.yaml up --build

# Выбор другой физической GPU:
GPU_DEVICE_ID=1 docker compose -f compose.gpu.yaml up --build
```

`GPU_DEVICE_ID` — GPU хоста. Внутри контейнера выбранное устройство отображается
как CUDA device 0. Также можно ограничивать устройства через NVIDIA Container Toolkit.
GPU-конфигурация сохраняет FP32 и не включает квантование.

Отдельный запуск без Compose:

```bash
make image-gpu
docker run --rm --gpus '"device=0"' -p 8080:8080 \
  -v "$PWD/models:/app/models:ro" vks-stt-go:cuda
```

Переключение с CPU на GPU:

```bash
docker compose -f compose.cpu.yaml down
docker compose -f compose.gpu.yaml up -d --build
```

Обратно — `down` GPU-конфигурации и `up` CPU-конфигурации. Порт, protobuf,
WebSocket JSON-RPC и формат результатов одинаковы. Буферы незавершённых сессий
при перезапуске не сохраняются.

## Нативные библиотеки

Go API закреплён на sherpa-onnx **1.13.8**. Go-программы компилируются с его
штатным C API. GPU-образ заменяет CPU-библиотеки одноимёнными GPU-библиотеками
**той же версии sherpa**, включая ONNX Runtime **1.28.2**, CUDA 12 и cuDNN 9.
Версия Go API проверяется скриптом установки; архив проверяется SHA-256.

Источник GPU runtime:
https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-v1.13.8-cuda-12.x-cudnn-9.x-onnxruntime1.28.2-linux-x64-gpu.tar.bz2

SHA-256: `2132af1ae8c84c84f86ac091e4311839287851d2bf5e660332ebce3898a1e0b2`.

Для обычной сборки на хосте:

```bash
make build-gpu
MODEL_PROVIDER=cuda ./bin/stt-server
```

На хосте при этом уже должны быть доступны совместимые CUDA 12 и cuDNN 9.
`SHERPA_CUDA_ARCHIVE=/path/runtime.tar.bz2 make build-gpu` позволяет использовать
локальный архив с тем же проверяемым SHA-256. `make build` возвращает CPU bundle.

## Проверка запуска и точности

При `MODEL_PROVIDER=cuda` проверяется именно загруженный ONNX Runtime:
наличие CUDAExecutionProvider, драйвера, устройства и загрузка CUDA provider.
При ошибке сервер не сообщает готовность и не подменяет GPU режимом CPU.
ONNX Runtime по-прежнему может назначать отдельные вспомогательные операции CPU.

`NVIDIA_TF32_OVERRIDE=0` устанавливается до инициализации CUDA, чтобы сохранить
политику FP32 вычислений. Веса модели не преобразуются при смене backend.
Это не обещание побитового совпадения CPU/GPU: порядок вычислений отличается.
Качество нужно сверять с исходной моделью на одних и тех же записях.

Замер на машине с GPU:

```bash
docker compose -f compose.gpu.yaml run --rm --no-deps \
  --entrypoint /app/stt-bench stt \
  -file /app/models/test.wav -threads 1,2,4 -runs 5
```

Поместите тестовый файл в смонтированную директорию models. JSONL содержит
`provider`, `precision`, путь энкодера, время загрузки, итерации и RTF.
Значения CPU/INT8 из предыдущих замеров не являются измерениями CUDA/FP32.

Официальная документация сборки sherpa CUDA:
https://k2-fsa.github.io/sherpa/onnx/install/linux.html
