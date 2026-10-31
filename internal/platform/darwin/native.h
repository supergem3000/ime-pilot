#include <stdint.h>
#include <stdlib.h>

char *ime_apps(void);
char *ime_app_info(const char *bundle_id);
char *ime_sources(void);
char *ime_snapshot(int manual);
int ime_select(uint32_t pid, uint64_t epoch, uint64_t window, const char *previous, const char *target, uint64_t deadline);
uint64_t ime_now(void);
uint64_t ime_epoch(void);
void ime_monitor_start(void);
void ime_monitor_stop(void);
