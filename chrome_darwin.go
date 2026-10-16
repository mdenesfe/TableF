//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// The window draws the page under a transparent title bar with an empty
// toolbar, so the traffic lights sit inset, centred on the app's 52pt header.
//
// In fullscreen macOS moves the title bar into a separate bar window that it
// shows under the menu bar, opaque, on top of the page — covering the header.
// So while fullscreen that bar is kept invisible and click-through, and the
// header draws the traffic lights itself in the same place (FullscreenLights
// in the frontend). macOS may rebuild the bar, so this is re-applied while
// fullscreen.
static NSTimer *tablefTimer;

static NSWindow *tablefBar(NSWindow *w) {
	NSWindow *bar = [w standardWindowButton:NSWindowCloseButton].window;
	return bar == w ? nil : bar;
}

// Makes the bar draw nothing: its content views are hidden (so even when
// macOS resets its transparency while sliding it in, there is nothing to see)
// and it lets every click through to the page.
static void tablefSilence(NSWindow *bar) {
	if (!bar) return;
	if (bar.alphaValue != 0) bar.alphaValue = 0;
	if (!bar.ignoresMouseEvents) bar.ignoresMouseEvents = YES;
	if (bar.opaque) bar.opaque = NO;
	NSView *frame = bar.contentView.superview ?: bar.contentView;
	for (NSView *v in frame.subviews) if (!v.hidden) v.hidden = YES;
}

static void tablefRestore(NSWindow *bar) {
	if (!bar) return;
	NSView *frame = bar.contentView.superview ?: bar.contentView;
	for (NSView *v in frame.subviews) v.hidden = NO;
	bar.ignoresMouseEvents = NO;
	bar.alphaValue = 1;
}

static void tablefManageFullscreenBar(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
		// From the start of the transition, so the bar never gets drawn once.
		[nc addObserverForName:NSWindowWillEnterFullScreenNotification object:nil queue:nil usingBlock:^(NSNotification *n) {
			NSWindow *w = n.object;
			[tablefTimer invalidate];
			tablefTimer = [NSTimer timerWithTimeInterval:1.0 / 120 repeats:YES block:^(NSTimer *t) { tablefSilence(tablefBar(w)); }];
			// Common modes: keep running during the fullscreen animation and menu tracking.
			[NSRunLoop.mainRunLoop addTimer:tablefTimer forMode:NSRunLoopCommonModes];
		}];
		[nc addObserverForName:NSWindowWillExitFullScreenNotification object:nil queue:nil usingBlock:^(NSNotification *n) {
			[tablefTimer invalidate];
			tablefTimer = nil;
			tablefRestore(tablefBar(n.object));
		}];
	});
}
*/
import "C"

import "context"

// manageWindowChrome installs platform tweaks to the native window.
func manageWindowChrome(context.Context) { C.tablefManageFullscreenBar() }
