#include "bridge.h"
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

struct stt_nemo {
    void *library;
    nemo_speech_diar_model *model;
    __typeof__(nemo_speech_diar_create) *create;
    __typeof__(nemo_speech_diar_destroy) *destroy;
    __typeof__(nemo_speech_diar_num_speakers) *speakers;
    __typeof__(nemo_speech_diar_stream_open) *open;
    __typeof__(nemo_speech_diar_stream_push_f32) *push;
    __typeof__(nemo_speech_diar_stream_finish) *finish;
    __typeof__(nemo_speech_diar_stream_close) *close;
    __typeof__(nemo_speech_diar_segments) *segments;
    __typeof__(nemo_speech_asr_last_error) *last_error;
};

// Copy the SDK's thread-local error before returning across cgo, where the Go
// goroutine may subsequently resume on a different OS thread.
static int checked(stt_nemo *a, int status, char *error, size_t n) {
    if (status) snprintf(error, n, "NeMo status %d: %s", status, a->last_error());
    return status;
}

stt_nemo *stt_nemo_create(const char *library, const char *model, char *error, size_t n) {
    stt_nemo *a = calloc(1, sizeof(*a));
    if (!a) { snprintf(error, n, "allocate NeMo API"); return NULL; }
    a->library = dlopen(library, RTLD_NOW | RTLD_LOCAL);
    if (!a->library) { snprintf(error, n, "load NeMo SDK: %s", dlerror()); free(a); return NULL; }
#define LOAD(field, symbol) do { *(void **)(&a->field) = dlsym(a->library, #symbol); \
    if (!a->field) { snprintf(error, n, "missing SDK symbol %s", #symbol); goto fail; } } while (0)
    LOAD(create, nemo_speech_diar_create);
    LOAD(destroy, nemo_speech_diar_destroy);
    LOAD(speakers, nemo_speech_diar_num_speakers);
    LOAD(open, nemo_speech_diar_stream_open);
    LOAD(push, nemo_speech_diar_stream_push_f32);
    LOAD(finish, nemo_speech_diar_stream_finish);
    LOAD(close, nemo_speech_diar_stream_close);
    LOAD(segments, nemo_speech_diar_segments);
    LOAD(last_error, nemo_speech_asr_last_error);
#undef LOAD
    nemo_speech_diar_model_config cfg = {0};
    cfg.size = sizeof(cfg);
    cfg.model_path = model;
    cfg.gpu = -1; // CPU first, including when GigaAM uses CUDA.
    cfg.preset = "v3-offline";
    cfg.left_context_frames = -1;
    if (checked(a, a->create(&cfg, &a->model), error, n)) goto fail;
    if (a->speakers(a->model) != 8) {
        snprintf(error, n, "expected an 8-speaker Nemotron-3 model"); goto fail;
    }
    return a;
fail:
    if (a->model && a->destroy) a->destroy(a->model);
    dlclose(a->library); free(a); return NULL;
}
void stt_nemo_destroy(stt_nemo *a) {
    if (a) { a->destroy(a->model); dlclose(a->library); free(a); }
}
nemo_speech_diar_stream *stt_nemo_open(stt_nemo *a, char *error, size_t n) {
    nemo_speech_diar_stream *s = NULL;
    return checked(a, a->open(a->model, &s), error, n) ? NULL : s;
}
int stt_nemo_push(stt_nemo *a, nemo_speech_diar_stream *s, const float *pcm, size_t count, char *error, size_t n) {
    return checked(a, a->push(s, pcm, count, 16000), error, n);
}
int stt_nemo_finish(stt_nemo *a, nemo_speech_diar_stream *s, char *error, size_t n) {
    return checked(a, a->finish(s), error, n);
}
int stt_nemo_segments(stt_nemo *a, nemo_speech_diar_stream *s, nemo_speech_diar_segment **out, size_t *count, char *error, size_t n) {
    *out = NULL; *count = 0;
    int status = a->segments(s, NULL, NULL, 0, count);
    if (checked(a, status, error, n) || !*count) return status;
    if (*count > SIZE_MAX / sizeof(**out)) { snprintf(error, n, "too many speaker segments"); return -1; }
    *out = calloc(*count, sizeof(**out));
    if (!*out) { snprintf(error, n, "allocate speaker segments"); return -1; }
    status = checked(a, a->segments(s, NULL, *out, *count, count), error, n);
    if (status) { free(*out); *out = NULL; }
    return status;
}
void stt_nemo_close(stt_nemo *a, nemo_speech_diar_stream *s) { a->close(s); }
