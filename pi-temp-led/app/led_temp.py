#!/usr/bin/env python3
"""CPU-load (or temperature) to RGB LED driver.

Reads this node's own CPU load (default) or CPU temperature and drives an
RGB LED on GPIO (BCM pins, default 12/16/20) through a blue (idle/cold) ->
red (busy/hot) gradient via software PWM. Originally a bash/Python experiment from ~2018-2019 on the
same 4-Pi cluster - rewritten to run as a Kubernetes DaemonSet, one instance
per node, each driving its own physically-attached LED.
"""

import os
import signal
import sys
import time

import RPi.GPIO as GPIO

RED_PIN = int(os.environ.get("LED_RED_PIN", "12"))
GREEN_PIN = int(os.environ.get("LED_GREEN_PIN", "16"))
BLUE_PIN = int(os.environ.get("LED_BLUE_PIN", "20"))

# What the color follows: "load" (CPU utilisation, default) or "temp".
SOURCE = os.environ.get("LED_SOURCE", "load")

TEMP_MIN_C = float(os.environ.get("LED_TEMP_MIN_C", "35"))  # full blue at/below this
TEMP_MAX_C = float(os.environ.get("LED_TEMP_MAX_C", "75"))  # full red at/above this

# A Pi in this cluster idles at 1-10% CPU with short spikes to 20-30%, so a
# linear 0-100% scale would keep the LED blue almost all the time. "sqrt"
# stretches the low end (4% -> 20% red, 25% -> 50%, only 100% is full red);
# "linear" is available for busier machines.
LOAD_CURVE = os.environ.get("LED_LOAD_CURVE", "sqrt")

# Exponential smoothing of the load reading, 0..1: higher follows changes
# faster, lower is calmer. Keeps per-second noise from turning into flicker.
LOAD_SMOOTHING = float(os.environ.get("LED_LOAD_SMOOTHING", "0.35"))
PWM_FREQ_HZ = int(os.environ.get("LED_PWM_FREQ_HZ", "100"))
POLL_SECONDS = float(os.environ.get("LED_POLL_SECONDS", "1"))

# On every (re)start, step through each color alone so a human watching
# the physical LED can confirm it's wired to the pin the logs claim -
# catches a swapped wire that a temperature-driven gradient would hide.
# 0 disables it.
SELFTEST_SECONDS = float(os.environ.get("LED_SELFTEST_SECONDS", "3"))

# Common-anode RGB LEDs sink current instead of sourcing it: a "duty cycle"
# of 100 (fully on, common-cathode) needs to be sent as 0 (fully on, common-
# anode) instead. Set LED_COMMON_ANODE=1 if that's how yours is wired.
COMMON_ANODE = os.environ.get("LED_COMMON_ANODE", "0") == "1"

THERMAL_ZONE = "/sys/class/thermal/thermal_zone0/temp"

# /proc/stat inside the container is the host's, so this is whole-node load.
PROC_STAT = "/proc/stat"


def read_cpu_temp_c():
    with open(THERMAL_ZONE) as f:
        return int(f.read().strip()) / 1000.0


def read_cpu_times():
    """Returns (total, idle) jiffies across all CPUs; idle includes iowait."""
    with open(PROC_STAT) as f:
        fields = [int(x) for x in f.readline().split()[1:]]
    return sum(fields), fields[3] + fields[4]


class LoadMeter:
    """CPU utilisation (0..1) since the previous call, smoothed."""

    def __init__(self):
        self.prev = read_cpu_times()
        self.smoothed = None

    def read(self):
        total, idle = read_cpu_times()
        d_total, d_idle = total - self.prev[0], idle - self.prev[1]
        self.prev = (total, idle)
        load = 1.0 - d_idle / d_total if d_total > 0 else 0.0
        load = max(0.0, min(1.0, load))
        if self.smoothed is None:
            self.smoothed = load
        else:
            self.smoothed += LOAD_SMOOTHING * (load - self.smoothed)
        return self.smoothed


def load_to_frac(load):
    return load ** 0.5 if LOAD_CURVE == "sqrt" else load


def temp_to_frac(temp_c):
    span = TEMP_MAX_C - TEMP_MIN_C
    frac = (temp_c - TEMP_MIN_C) / span if span else 0.0
    return max(0.0, min(1.0, frac))


def frac_to_duty_cycles(frac):
    """0 -> full blue, 1 -> full red; no green, a straight two-color blend."""
    red = frac * 100
    blue = (1.0 - frac) * 100
    green = 0.0

    if COMMON_ANODE:
        red, green, blue = 100 - red, 100 - green, 100 - blue

    return red, green, blue


def main():
    GPIO.setmode(GPIO.BCM)
    GPIO.setwarnings(False)
    GPIO.setup(RED_PIN, GPIO.OUT)
    GPIO.setup(GREEN_PIN, GPIO.OUT)
    GPIO.setup(BLUE_PIN, GPIO.OUT)

    red_pwm = GPIO.PWM(RED_PIN, PWM_FREQ_HZ)
    green_pwm = GPIO.PWM(GREEN_PIN, PWM_FREQ_HZ)
    blue_pwm = GPIO.PWM(BLUE_PIN, PWM_FREQ_HZ)
    red_pwm.start(0)
    green_pwm.start(0)
    blue_pwm.start(0)

    def shutdown(signum, frame):
        red_pwm.stop()
        green_pwm.stop()
        blue_pwm.stop()
        GPIO.cleanup()
        sys.exit(0)

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)

    node = os.environ.get("NODE_NAME", "unknown-node")
    if SOURCE == "temp":
        print(f"[{node}] watching {THERMAL_ZONE}, {TEMP_MIN_C}C=blue .. {TEMP_MAX_C}C=red", flush=True)
    else:
        print(f"[{node}] watching CPU load, curve={LOAD_CURVE} smoothing={LOAD_SMOOTHING}, idle=blue .. 100%=red", flush=True)

    def set_pwm(red, green, blue):
        if COMMON_ANODE:
            red, green, blue = 100 - red, 100 - green, 100 - blue
        red_pwm.ChangeDutyCycle(red)
        green_pwm.ChangeDutyCycle(green)
        blue_pwm.ChangeDutyCycle(blue)

    if SELFTEST_SECONDS > 0:
        for label, (r, g, b) in [
            ("RED", (100, 0, 0)),
            ("GREEN", (0, 100, 0)),
            ("BLUE", (0, 0, 100)),
            ("OFF", (0, 0, 0)),
        ]:
            print(f"[{node}] selftest: {label} pin={RED_PIN if label == 'RED' else GREEN_PIN if label == 'GREEN' else BLUE_PIN if label == 'BLUE' else '-'}", flush=True)
            set_pwm(r, g, b)
            time.sleep(SELFTEST_SECONDS)

    meter = LoadMeter() if SOURCE != "temp" else None
    while True:
        time.sleep(POLL_SECONDS)
        if meter:
            load = meter.read()
            frac = load_to_frac(load)
            reading = f"load={load * 100:.0f}%"
        else:
            temp_c = read_cpu_temp_c()
            frac = temp_to_frac(temp_c)
            reading = f"cpu={temp_c:.1f}C"
        red, green, blue = frac_to_duty_cycles(frac)
        red_pwm.ChangeDutyCycle(red)
        green_pwm.ChangeDutyCycle(green)
        blue_pwm.ChangeDutyCycle(blue)
        print(f"[{node}] {reading} red={red:.0f} green={green:.0f} blue={blue:.0f}", flush=True)


if __name__ == "__main__":
    main()
