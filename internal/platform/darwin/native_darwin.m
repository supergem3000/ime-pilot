#import <AppKit/AppKit.h>
#import <Carbon/Carbon.h>
#include <stdatomic.h>
#include <time.h>
#include "native.h"

extern void goForegroundChanged(uint32_t pid, char *bundle, uint64_t epoch, int baseline);

static _Atomic(uint64_t) generation;
static NSMutableArray *observers;
static NSMutableArray *applicationObservers;
static BOOL sessionActive = YES;
static void emit(NSRunningApplication *app, BOOL baseline);

// 手动测试本体窗口时，NSWorkspace 可能仍报告上一个普通应用。
static NSRunningApplication *manualApplication(void) {
    return NSApp.isActive ? NSRunningApplication.currentApplication : NSWorkspace.sharedWorkspace.frontmostApplication;
}

static void onMain(dispatch_block_t block) {
    if ([NSThread isMainThread]) block();
    else dispatch_sync(dispatch_get_main_queue(), block);
}

static char *json(id value) {
    NSData *data = [NSJSONSerialization dataWithJSONObject:value options:0 error:nil];
    return data ? strndup(data.bytes, data.length) : NULL;
}

static NSString *property(TISInputSourceRef source, CFStringRef key) {
    return (__bridge NSString *)TISGetInputSourceProperty(source, key) ?: @"";
}

static NSArray *sourceList(void) {
    NSDictionary *filter = @{
        (__bridge NSString *)kTISPropertyInputSourceCategory: (__bridge NSString *)kTISCategoryKeyboardInputSource,
        (__bridge NSString *)kTISPropertyInputSourceIsSelectCapable: @YES,
        (__bridge NSString *)kTISPropertyInputSourceIsEnabled: @YES
    };
    return CFBridgingRelease(TISCreateInputSourceList((__bridge CFDictionaryRef)filter, false));
}

char *ime_apps(void) {
    __block char *result;
    onMain(^{ @autoreleasepool {
        NSMutableArray *apps = [NSMutableArray array];
        for (NSRunningApplication *app in NSWorkspace.sharedWorkspace.runningApplications) {
            if (app.activationPolicy != NSApplicationActivationPolicyRegular ||
                app.processIdentifier == getpid() || !app.bundleIdentifier.length) continue;
            [apps addObject:@{@"name": app.localizedName ?: app.bundleIdentifier, @"bundleId": app.bundleIdentifier}];
        }
        result = json(apps);
    }});
    return result;
}

// Resolve installed apps, including those that are not currently running.
// AppKit image work stays on the main thread and never launches the app.
char *ime_app_info(const char *bundle_id) {
    NSString *identifier = [NSString stringWithUTF8String:bundle_id];
    __block char *result = NULL;
    onMain(^{ @autoreleasepool {
        NSWorkspace *workspace = NSWorkspace.sharedWorkspace;
        NSURL *url = [workspace URLForApplicationWithBundleIdentifier:identifier];
        if (!url) return;
        NSString *name = [NSFileManager.defaultManager displayNameAtPath:url.path];
        if ([name.pathExtension.lowercaseString isEqualToString:@"app"]) {
            name = name.stringByDeletingPathExtension;
        }

        NSImage *icon = [workspace iconForFile:url.path];
        NSBitmapImageRep *bitmap = [[NSBitmapImageRep alloc]
            initWithBitmapDataPlanes:NULL pixelsWide:64 pixelsHigh:64
            bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO
            colorSpaceName:NSCalibratedRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
        NSString *dataURL = @"";
        NSGraphicsContext *context = bitmap ? [NSGraphicsContext graphicsContextWithBitmapImageRep:bitmap] : nil;
        if (icon && context) {
            [NSGraphicsContext saveGraphicsState];
            NSGraphicsContext.currentContext = context;
            [icon drawInRect:NSMakeRect(0, 0, 64, 64) fromRect:NSZeroRect
                operation:NSCompositingOperationCopy fraction:1.0];
            [NSGraphicsContext restoreGraphicsState];
            NSData *png = [bitmap representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
            if (png) dataURL = [@"data:image/png;base64," stringByAppendingString:[png base64EncodedStringWithOptions:0]];
        }
        result = json(@{@"name": name.length ? name : identifier, @"icon": dataURL});
    }});
    return result;
}

char *ime_sources(void) {
    __block char *result;
    onMain(^{ @autoreleasepool {
        NSMutableArray *sources = [NSMutableArray array];
        for (id item in sourceList()) {
            TISInputSourceRef source = (__bridge TISInputSourceRef)item;
            NSArray *languages = (__bridge NSArray *)TISGetInputSourceProperty(source, kTISPropertyInputSourceLanguages);
            BOOL chinese = NO;
            for (NSString *language in languages) {
                if ([language isEqualToString:@"zh"] || [language hasPrefix:@"zh-"]) chinese = YES;
            }
            BOOL english = CFEqual(TISGetInputSourceProperty(source, kTISPropertyInputSourceType), kTISTypeKeyboardLayout) &&
                TISGetInputSourceProperty(source, kTISPropertyInputSourceIsASCIICapable) == kCFBooleanTrue;
            [sources addObject:@{@"id": property(source, kTISPropertyInputSourceID),
                @"name": property(source, kTISPropertyLocalizedName), @"chinese": @(chinese), @"english": @(english)}];
        }
        result = json(sources);
    }});
    return result;
}

char *ime_snapshot(int manual) {
    __block char *result;
    onMain(^{ @autoreleasepool {
        NSRunningApplication *app = manual ? manualApplication() : NSWorkspace.sharedWorkspace.frontmostApplication;
        TISInputSourceRef source = TISCopyCurrentKeyboardInputSource();
        if (!source) { result = NULL; return; }
        NSInteger window = manual && app.processIdentifier == getpid() ? NSApp.keyWindow.windowNumber : 0;
        result = json(@{@"pid": @(sessionActive ? app.processIdentifier : 0), @"bundleId": app.bundleIdentifier ?: @"",
            @"epoch": @(atomic_load(&generation)), @"window": @(window),
            @"sourceId": property(source, kTISPropertyInputSourceID), @"name": property(source, kTISPropertyLocalizedName)});
        CFRelease(source);
    }});
    return result;
}

uint64_t ime_now(void) { return clock_gettime_nsec_np(CLOCK_MONOTONIC_RAW); }
uint64_t ime_epoch(void) { return atomic_load(&generation); }

int ime_select(uint32_t pid, uint64_t epoch, uint64_t window, const char *previous, const char *target, uint64_t deadline) {
    __block int result = -1;
    onMain(^{ @autoreleasepool {
        if (ime_now() >= deadline) { result = -4; return; }
        NSRunningApplication *app = window ? manualApplication() : NSWorkspace.sharedWorkspace.frontmostApplication;
        if (!sessionActive || app.processIdentifier != pid || atomic_load(&generation) != epoch ||
            (window && (pid != getpid() || NSApp.keyWindow.windowNumber != window))) { result = -2; return; }
        TISInputSourceRef current = TISCopyCurrentKeyboardInputSource();
        if (!current) return;
        BOOL unchanged = [property(current, kTISPropertyInputSourceID) isEqualToString:@(previous)];
        CFRelease(current);
        if (!unchanged) { result = -2; return; }
        for (id item in sourceList()) {
            TISInputSourceRef source = (__bridge TISInputSourceRef)item;
            if ([property(source, kTISPropertyInputSourceID) isEqualToString:@(target)]) {
                if (ime_now() >= deadline) { result = -4; return; }
                result = TISSelectInputSource(source) == noErr ? 0 : -3;
                return;
            }
        }
    }});
    return result;
}

static void emit(NSRunningApplication *app, BOOL baseline) {
    uint64_t epoch = atomic_fetch_add(&generation, 1) + 1;
    goForegroundChanged(app ? app.processIdentifier : 0, (char *)(app.bundleIdentifier ?: @"").UTF8String, epoch, baseline);
}

void ime_monitor_start(void) {
    onMain(^{
        NSNotificationCenter *center = NSWorkspace.sharedWorkspace.notificationCenter;
        observers = [NSMutableArray array];
        applicationObservers = [NSMutableArray array];
        for (NSString *name in @[NSApplicationDidBecomeActiveNotification, NSApplicationDidResignActiveNotification]) {
            [applicationObservers addObject:[NSNotificationCenter.defaultCenter addObserverForName:name object:NSApp queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
                if (sessionActive) emit(NSWorkspace.sharedWorkspace.frontmostApplication, NO);
            }]];
        }
        [observers addObject:[center addObserverForName:NSWorkspaceDidActivateApplicationNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
            if (sessionActive) emit(note.userInfo[NSWorkspaceApplicationKey], NO);
        }]];
        [observers addObject:[center addObserverForName:NSWorkspaceDidDeactivateApplicationNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
            emit(nil, NO);
        }]];
        for (NSString *name in @[NSWorkspaceSessionDidResignActiveNotification, NSWorkspaceWillSleepNotification]) {
            [observers addObject:[center addObserverForName:name object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
                sessionActive = NO;
                emit(nil, YES);
            }]];
        }
        for (NSString *name in @[NSWorkspaceSessionDidBecomeActiveNotification, NSWorkspaceDidWakeNotification]) {
            [observers addObject:[center addObserverForName:name object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
                sessionActive = YES;
                emit(NSWorkspace.sharedWorkspace.frontmostApplication, YES);
            }]];
        }
        emit(NSWorkspace.sharedWorkspace.frontmostApplication, YES);
    });
}

void ime_monitor_stop(void) {
    onMain(^{
        for (id observer in observers) [NSWorkspace.sharedWorkspace.notificationCenter removeObserver:observer];
        for (id observer in applicationObservers) [NSNotificationCenter.defaultCenter removeObserver:observer];
        observers = nil;
        applicationObservers = nil;
        atomic_fetch_add(&generation, 1);
    });
}
