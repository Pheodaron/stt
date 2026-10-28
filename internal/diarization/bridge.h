#ifndef STT_NEMO_BRIDGE_H
#define STT_NEMO_BRIDGE_H
#include "sdk/nemo_speech/diar.h"
typedef struct stt_nemo stt_nemo;
stt_nemo *stt_nemo_create(const char *library, const char *model, char *error, size_t error_size);
void stt_nemo_destroy(stt_nemo *api);
nemo_speech_diar_stream *stt_nemo_open(stt_nemo *api, char *error, size_t error_size);
int stt_nemo_push(stt_nemo *api, nemo_speech_diar_stream *stream, const float *pcm, size_t count, char *error, size_t error_size);
int stt_nemo_finish(stt_nemo *api, nemo_speech_diar_stream *stream, char *error, size_t error_size);
int stt_nemo_segments(stt_nemo *api, nemo_speech_diar_stream *stream, nemo_speech_diar_segment **segments, size_t *count, char *error, size_t error_size);
void stt_nemo_close(stt_nemo *api, nemo_speech_diar_stream *stream);
#endif
