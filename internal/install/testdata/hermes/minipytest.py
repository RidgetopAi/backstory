"""Stdlib-only stand-in for the slice of pytest test_provider.py uses
(fixture, monkeypatch, tmp_path), so `make check` still runs the provider
suite on machines without pytest. Usage: minipytest.py test_file.py"""
import contextlib
import importlib.util
import inspect
import os
import sys
import tempfile
import traceback
import types
from pathlib import Path


def fixture(*a, **kw):
    def mark(fn):
        fn._is_fixture = True
        return fn
    return mark(a[0]) if a and callable(a[0]) else mark


class Monkeypatch:
    def __init__(self):
        self._undo = []

    def setitem(self, d, k, v):
        self._undo.append((d, k, d.get(k, KeyError)))
        d[k] = v

    def setenv(self, k, v):
        self.setitem(os.environ, k, str(v))

    def undo(self):
        for d, k, old in reversed(self._undo):
            if old is KeyError:
                d.pop(k, None)
            else:
                d[k] = old


def run(path):
    shim = types.ModuleType("pytest")
    shim.fixture = fixture
    sys.modules["pytest"] = shim
    spec = importlib.util.spec_from_file_location("suite", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    failed = passed = 0
    for name, fn in list(vars(mod).items()):
        if not (name.startswith("test_") and callable(fn)):
            continue
        with contextlib.ExitStack() as stack:
            cache = {}

            def resolve(arg):
                if arg in cache:
                    return cache[arg]
                if arg == "monkeypatch":
                    mp = Monkeypatch()
                    stack.callback(mp.undo)
                    val = mp
                elif arg == "tmp_path":
                    val = Path(stack.enter_context(tempfile.TemporaryDirectory()))
                else:
                    f = getattr(mod, arg)
                    val = f(*[resolve(a) for a in inspect.signature(f).parameters])
                    if inspect.isgenerator(val):
                        gen = val
                        val = next(gen)
                        stack.callback(lambda g=gen: list(g))
                cache[arg] = val
                return val

            try:
                fn(*[resolve(a) for a in inspect.signature(fn).parameters])
                passed += 1
            except BaseException:
                failed += 1
                print("FAILED", name)
                traceback.print_exc()
    print(f"{passed} passed, {failed} failed")
    return 1 if failed or not passed else 0


if __name__ == "__main__":
    sys.exit(run(sys.argv[1]))
