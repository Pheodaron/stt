//go:build linux

#include "provider_linux.h"
#include "ort/onnxruntime_c_api.h"

#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

static int copy_ort_error(const OrtApi *api, OrtStatus *status,
                          char *message, size_t size) {
    if (!status) {
        return 0;
    }

    snprintf(message, size, "%s", api->GetErrorMessage(status));
    api->ReleaseStatus(status);
    return 1;
}

static int require_cuda_provider(const OrtApi *api, char *message, size_t size) {
    char **providers = NULL;
    int count = 0;
    int has_cuda = 0;

    OrtStatus *status = api->GetAvailableProviders(&providers, &count);
    if (copy_ort_error(api, status, message, size)) {
        return 1;
    }

    for (int i = 0; i < count; ++i) {
        if (strcmp(providers[i], "CUDAExecutionProvider") == 0) {
            has_cuda = 1;
        }
    }

    status = api->ReleaseAvailableProviders(providers, count);
    if (status) {
        api->ReleaseStatus(status);
    }

    if (!has_cuda) {
        snprintf(message, size,
                 "loaded ONNX Runtime has no CUDAExecutionProvider; "
                 "use Dockerfile.gpu or install the CUDA native libraries");
        return 1;
    }

    return 0;
}

static void *open_cuda_driver(char *message, size_t size) {
    void *driver = dlopen("libcuda.so.1", RTLD_NOW | RTLD_LOCAL);
    if (!driver) {
        snprintf(message, size, "NVIDIA driver is unavailable: %s", dlerror());
        return NULL;
    }

    typedef int (*init_fn)(unsigned int);
    typedef int (*count_fn)(int *);

    init_fn initialize = (init_fn)dlsym(driver, "cuInit");
    count_fn device_count = (count_fn)dlsym(driver, "cuDeviceGetCount");
    int devices = 0;

    if (!initialize || !device_count || initialize(0) != 0 ||
        device_count(&devices) != 0 || devices < 1) {
        snprintf(message, size,
                 "no usable NVIDIA CUDA device; expose a GPU to the container");
        dlclose(driver);
        return NULL;
    }

    return driver;
}

static int check_cuda_options(const OrtApi *api, char *message, size_t size) {
    OrtEnv *environment = NULL;
    OrtSessionOptions *options = NULL;
    OrtCUDAProviderOptionsV2 *cuda = NULL;
    OrtStatus *status = NULL;
    int result = 1;

    status = api->CreateEnv(ORT_LOGGING_LEVEL_WARNING, "stt-cuda-check", &environment);
    if (copy_ort_error(api, status, message, size)) {
        goto cleanup;
    }

    status = api->CreateSessionOptions(&options);
    if (copy_ort_error(api, status, message, size)) {
        goto cleanup;
    }

    status = api->CreateCUDAProviderOptions(&cuda);
    if (copy_ort_error(api, status, message, size)) {
        goto cleanup;
    }

    const char *keys[] = {"device_id", "use_tf32", "cudnn_conv_algo_search"};
    const char *values[] = {"0", "0", "HEURISTIC"};

    status = api->UpdateCUDAProviderOptions(cuda, keys, values, 3);
    if (copy_ort_error(api, status, message, size)) {
        goto cleanup;
    }

    status = api->SessionOptionsAppendExecutionProvider_CUDA_V2(options, cuda);
    if (copy_ort_error(api, status, message, size)) {
        goto cleanup;
    }

    result = 0;

cleanup:
    if (cuda) {
        api->ReleaseCUDAProviderOptions(cuda);
    }
    if (options) {
        api->ReleaseSessionOptions(options);
    }
    if (environment) {
        api->ReleaseEnv(environment);
    }

    return result;
}

// Query the runtime sherpa actually loads: a provider string alone permits fallback.
int stt_check_cuda(char *message, size_t size) {
    const OrtApi *api = OrtGetApiBase()->GetApi(ORT_API_VERSION);
    if (!api) {
        snprintf(message, size, "ONNX Runtime C API 20 is unavailable");
        return 1;
    }

    if (require_cuda_provider(api, message, size)) {
        return 1;
    }

    void *driver = open_cuda_driver(message, size);
    if (!driver) {
        return 1;
    }

    int result = check_cuda_options(api, message, size);
    dlclose(driver);
    return result;
}
