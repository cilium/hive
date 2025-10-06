// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
)

type Config struct {
	Foo string
	Bar int
}

func (Config) Flags(flags *pflag.FlagSet) {
	flags.String("foo", "hello world", "sets the greeting")
	flags.Int("bar", 123, "bar")
}

func TestHiveGoodConfig(t *testing.T) {
	var cfg Config
	testCell := cell.Module(
		"test",
		"Test Module",
		cell.Config(Config{}),
		cell.Invoke(func(c Config) {
			cfg = c
		}),
	)

	hive := hive.New(testCell)

	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	hive.RegisterFlags(flags)

	// Test the two ways of setting it
	flags.Set("foo", "test")
	hive.Viper().Set("bar", 13)

	log := hivetest.Logger(t)
	err := hive.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")

	err = hive.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")

	assert.Equal(t, "test", cfg.Foo, "Config.Foo not set correctly")
	assert.Equal(t, 13, cfg.Bar, "Config.Bar not set correctly")
}

// BadConfig has a field that matches no flags, and Flags
// declares a flag that matches no field.
type BadConfig struct {
	Bar string
}

func (BadConfig) Flags(flags *pflag.FlagSet) {
	flags.String("foo", "foobar", "foo")
}

func TestHiveBadConfig(t *testing.T) {
	testCell := cell.Module(
		"test",
		"Test Module",
		cell.Config(BadConfig{}),
		cell.Invoke(func(c BadConfig) {}),
	)

	hive := hive.New(testCell)

	err := hive.Start(hivetest.Logger(t), context.TODO())
	assert.ErrorContains(t, err, "has invalid keys: foo", "expected 'invalid keys' error")
	assert.ErrorContains(t, err, "has unset fields: Bar", "expected 'unset fields' error")
}

type MapConfig struct {
	Foo map[string]string
}

func (MapConfig) Flags(flags *pflag.FlagSet) {
	flags.StringToString("foo", nil, "foo")
}

func TestHiveConfigOverride(t *testing.T) {
	var cfg Config
	h := hive.New(
		cell.Config(Config{}),
		cell.Invoke(func(c Config) {
			cfg = c
		}),
	)
	hive.AddConfigOverride(
		h,
		func(cfg *Config) {
			cfg.Foo = "override"
		})

	// Set "foo" flag via Viper. This should be ignored.
	h.Viper().Set("foo", "viper")

	log := hivetest.Logger(t)
	err := h.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")

	err = h.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")

	assert.Equal(t, "override", cfg.Foo, "Config.Foo not set correctly")
}

type StringSliceConfig struct {
	SpacesFlag, CommasFlag []string
	SpacesMap, CommasMap   []string
	Mixed                  []string
	StringFlag             string
}

func (StringSliceConfig) Flags(flags *pflag.FlagSet) {
	flags.StringSlice("spaces-flag", nil, "split by spaces via pflag")
	flags.StringSlice("commas-flag", nil, "split by commas via pflag")
	flags.StringSlice("spaces-map", nil, "split by spaces via configmap")
	flags.StringSlice("commas-map", nil, "split by commas via configmap")

	// One can also use just flags.String against a []string config field and
	// it will be parsed the same way as via configmap.
	flags.String("mixed", "", "mixed")

	flags.String("string-flag", "", "plain string untouched")
}

func TestHiveStringSlice(t *testing.T) {
	var cfg StringSliceConfig
	testCell := cell.Module(
		"test",
		"Test Module",
		cell.Config(StringSliceConfig{}),
		cell.Invoke(func(c StringSliceConfig) {
			cfg = c
		}),
	)
	hive := hive.New(testCell)

	flags := pflag.NewFlagSet("", pflag.ContinueOnError)
	hive.RegisterFlags(flags)

	spaces := "foo bar baz"
	commas := "foo,bar,baz"
	expected := []string{"foo", "bar", "baz"}

	// When the values are provided via the flags then the value given
	// to Viper is already a []string as parsed by pflag. This will be
	// processed by stringSliceToStringSliceHookFunc to re-split if needed.
	flags.Set("spaces-flag", spaces)
	flags.Set("commas-flag", commas)

	// Plain string flags are not split in any way.
	flags.Set("string-flag", "foo, bar, baz")

	// If spaces and commas are mixed then commas take precedence.
	flags.Set("mixed", "foo bar,baz")

	// When the values are provided via the configmap or environment they're
	// given to Viper as plain strings that will be processed by stringToSliceHookFunc.
	hive.Viper().MergeConfigMap(
		map[string]any{
			"spaces-map": spaces,
			"commas-map": commas,
		})

	log := hivetest.Logger(t)
	err := hive.Start(log, context.TODO())
	require.NoError(t, err, "expected Start to succeed")
	err = hive.Stop(log, context.TODO())
	require.NoError(t, err, "expected Stop to succeed")

	assert.ElementsMatch(t, cfg.SpacesFlag, expected, "unexpected SpacesFlag")
	assert.ElementsMatch(t, cfg.SpacesMap, expected, "unexpected SpacesMap")
	assert.ElementsMatch(t, cfg.CommasFlag, expected, "unexpected CommasFlag")
	assert.ElementsMatch(t, cfg.CommasMap, expected, "unexpected CommasMap")
	assert.ElementsMatch(t, cfg.Mixed, []string{"foo bar", "baz"}, "unexpected Mixed")
	assert.Equal(t, cfg.StringFlag, "foo, bar, baz", "unexpected StringFlag")
}

type SomeObject struct {
	X int
}

type OtherObject struct {
	Y int
}

func TestProvideInvoke(t *testing.T) {
	invoked := false

	testCell := cell.Module(
		"test",
		"Test Module",
		cell.Provide(func() *SomeObject { return &SomeObject{10} }),
		cell.Invoke(func(*SomeObject) { invoked = true }),
	)

	err := hive.New(
		testCell,
		shutdownOnStartCell,
	).Run(hivetest.Logger(t))
	assert.NoError(t, err, "expected Run to succeed")

	assert.True(t, invoked, "expected invoke to be called, but it was not")
}

func TestModuleID(t *testing.T) {
	invoked := false
	inner := cell.Module(
		"inner",
		"inner module",
		cell.Invoke(func(id cell.ModuleID, fid cell.FullModuleID) error {
			invoked = true
			if id != "inner" {
				return fmt.Errorf("inner id mismatch, expected 'inner', got %q", id)
			}
			if fid.String() != "outer.inner" {
				return fmt.Errorf("outer id mismatch, expected 'outer.inner', got %q", fid)
			}
			return nil
		}),
	)

	outer := cell.Module(
		"outer",
		"outer module",
		inner,
	)

	err := hive.New(
		outer,
		shutdownOnStartCell,
	).Run(hivetest.Logger(t))
	assert.NoError(t, err, "expected Run to succeed")

	assert.True(t, invoked, "expected invoke to be called, but it was not")
}

func TestGroup(t *testing.T) {
	sum := 0

	testCell := cell.Group(
		cell.Provide(func() *SomeObject { return &SomeObject{10} }),
		cell.Provide(func() *OtherObject { return &OtherObject{5} }),
	)
	err := hive.New(
		testCell,
		cell.Invoke(func(a *SomeObject, b *OtherObject) { sum = a.X + b.Y }),
		shutdownOnStartCell,
	).Run(hivetest.Logger(t))
	assert.NoError(t, err, "expected Run to succeed")
	assert.Equal(t, 15, sum)
}

func TestProvidePrivate(t *testing.T) {
	invoked := false

	testCell := cell.Module(
		"test",
		"Test Module",
		cell.ProvidePrivate(func() *SomeObject { return &SomeObject{10} }),
		cell.Invoke(func(*SomeObject) { invoked = true }),
	)

	// Test happy path.
	err := hive.New(
		testCell,
		shutdownOnStartCell,
	).Run(hivetest.Logger(t))
	assert.NoError(t, err, "expected Start to succeed")

	if !invoked {
		t.Fatal("expected invoke to be called, but it was not")
	}

	// Now test that we can't access it from root scope.
	h := hive.New(
		testCell,
		cell.Invoke(func(*SomeObject) {}),
		shutdownOnStartCell,
	)
	err = h.Start(hivetest.Logger(t), context.TODO())
	assert.ErrorContains(t, err, "missing type: *hive_test.SomeObject", "expected Start to fail to find *SomeObject")
}

func TestDecorate(t *testing.T) {
	invoked := false

	testCell := cell.Decorate(
		func(o *SomeObject) *SomeObject {
			return &SomeObject{X: o.X + 1}
		},
		cell.Invoke(
			func(o *SomeObject) error {
				if o.X != 2 {
					return errors.New("X != 2")
				}
				invoked = true
				return nil
			}),
	)

	hive := hive.New(
		cell.Provide(func() *SomeObject { return &SomeObject{1} }),

		// Here *SomeObject is not decorated.
		cell.Invoke(func(o *SomeObject) error {
			if o.X != 1 {
				return errors.New("X != 1")
			}
			return nil
		}),

		testCell,

		shutdownOnStartCell,
	)

	assert.NoError(t, hive.Run(hivetest.Logger(t)), "expected Run() to succeed")
	assert.True(t, invoked, "expected decorated invoke function to be called")
}

func TestDecorateAll(t *testing.T) {
	rootX, testX, test2Int := 0, 0, 0
	type rootInt int
	hive := hive.New(
		cell.Module("test", "test",
			cell.Provide(func() *SomeObject { return &SomeObject{1} }),
			cell.Invoke(func(o *SomeObject) { testX = o.X }),
		),
		cell.Module("test2", "test2",
			cell.Provide(func(o *SomeObject) int { return o.X }),
		),
		cell.Invoke(func(o *SomeObject, x int) {
			rootX = o.X
			test2Int = x
		}),
		cell.DecorateAll(
			func(o *SomeObject) *SomeObject {
				return &SomeObject{X: o.X + 1}
			},
		),

		shutdownOnStartCell,
	)

	assert.NoError(t, hive.Run(hivetest.Logger(t)), "expected Run() to succeed")
	assert.Equal(t, 2, rootX, "expected object at root scope to have X=2")
	assert.Equal(t, 2, testX, "expected object in test module scope to have X=2")
	assert.Equal(t, 2, test2Int, "expected object in test module scope to be 2")
}

func TestShutdown(t *testing.T) {
	//
	// Happy paths without a shutdown error:
	//

	// Test from a start hook
	h := hive.New(
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			lc.Append(cell.Hook{
				OnStart: func(cell.HookContext) error {
					shutdowner.Shutdown()
					return nil
				}})
		}),
	)
	assert.NoError(t, h.Run(hivetest.Logger(t)), "expected Run() to succeed")

	// Test from a goroutine forked from start hook
	h = hive.New(
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			lc.Append(cell.Hook{
				OnStart: func(cell.HookContext) error {
					go shutdowner.Shutdown()
					return nil
				}})
		}),
	)
	assert.NoError(t, h.Run(hivetest.Logger(t)), "expected Run() to succeed")

	// Test from an invoke. Shouldn't really be used, but should still work.
	h = hive.New(
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			shutdowner.Shutdown()
		}),
	)
	assert.NoError(t, h.Run(hivetest.Logger(t)), "expected Run() to succeed")

	//
	// Unhappy paths that fatal with an error:
	//

	shutdownErr := errors.New("shutdown error")

	// Test from a start hook
	h = hive.New(
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			lc.Append(cell.Hook{
				OnStart: func(cell.HookContext) error {
					shutdowner.Shutdown(hive.ShutdownWithError(shutdownErr))
					return nil
				}})
		}),
	)
	assert.ErrorIs(t, h.Run(hivetest.Logger(t)), shutdownErr, "expected Run() to fail with shutdownErr")
}

func TestTimeoutOverride(t *testing.T) {
	var started, stopped int
	opts := hive.DefaultOptions()

	newStartTimeout := 3*time.Millisecond
	newStopTimeout := 2*time.Millisecond

	h := hive.NewWithOptions(
		opts,
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			lc.Append(cell.Hook{
				OnStart: func(ctx cell.HookContext) error {
					started ++

					deadline, ok := ctx.Deadline()
					assert.True(t, ok, "expected start context to have a deadline")

					timeToDeadline := time.Until(deadline)
					assert.LessOrEqual(t, timeToDeadline, newStartTimeout, "expected time to start context deadline be less or equal than the overridden timeout")

					shutdowner.Shutdown()
					return nil
				},
				OnStop: func(ctx cell.HookContext) error {
					stopped ++

					deadline, ok := ctx.Deadline()
					assert.True(t, ok, "expexted stop context to have a deadline")

					timeToDeadline := time.Until(deadline)
					assert.LessOrEqual(t, timeToDeadline, newStopTimeout, "expected time to stop context deadline be less or equal than the overridden timeout")

					return nil
				},
			})
		}),
	)

	err := h.Run(
		hivetest.Logger(t),
		hive.WithStartTimeout(newStartTimeout),
		hive.WithStopTimeout(newStopTimeout),
	)

	assert.NoError(t, err, "expected Run() to succeed")

	assert.Equal(t, 1, started)
	assert.Equal(t, 1, stopped)
}

func TestLogThresholdOverride(t *testing.T) {
	invoked := false
	newLogThreshold := time.Millisecond

	h := hive.New(
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			lc.Append(cell.Hook{
				OnStart: func(cell.HookContext) error {
					shutdowner.Shutdown()
					return nil
				}})
		}),
	)

	h.AppendInvoke(func(_ *slog.Logger, threshold time.Duration) error {
		invoked = true
		assert.Equal(t, newLogThreshold, threshold, "expected logThreshold to be equal to overridden value")
		return nil
	})

	err := h.Run(
		hivetest.Logger(t),
		hive.WithLogThreshold(newLogThreshold),
	)

	assert.NoError(t, err, "expected Run() to succeed")

	assert.True(t, invoked)
}

func TestRunRollback(t *testing.T) {
	var started, stopped int
	opts := hive.DefaultOptions()
	opts.StartTimeout = time.Millisecond
	h := hive.NewWithOptions(
		opts,
		cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
			lc.Append(cell.Hook{
				OnStart: func(ctx cell.HookContext) error {
					started++
					return nil
				},
				OnStop: func(ctx cell.HookContext) error {
					stopped++
					return nil
				},
			})
			lc.Append(cell.Hook{
				OnStart: func(ctx cell.HookContext) error {
					started++
					<-ctx.Done()
					return ctx.Err()
				},
				OnStop: func(cell.HookContext) error {
					// Should not be called.
					t.Fatal("unexpected call to second OnStop")
					return nil
				},
			})
		}),
	)

	err := h.Run(hivetest.Logger(t))
	assert.ErrorIs(t, err, context.DeadlineExceeded, "expected Run() to fail with timeout")

	// We should see 2 start hooks invoked, and then 1 stop hook as first
	// one is rolled back.
	assert.Equal(t, 2, started)
	assert.Equal(t, 1, stopped)
}

var shutdownOnStartCell = cell.Invoke(func(lc cell.Lifecycle, shutdowner hive.Shutdowner) {
	lc.Append(cell.Hook{
		OnStart: func(cell.HookContext) error {
			shutdowner.Shutdown()
			return nil
		}})
})

// Assert that we can reuse the same cell as part of multiple hives
func TestSameCellMultipleHives(t *testing.T) {
	var (
		got1 string
		got2 int
	)

	common := cell.Group(
		cell.Config(Config{}),
		cell.Provide(func() int { return 10 }),
		cell.Invoke(func(_ Config, in1 string, in2 int) { got1, got2 = in1, in2 }),
	)

	h1 := hive.New(common, cell.Provide(func() string { return "foo" }))
	h2 := hive.New(common, cell.Provide(func() string { return "bar" }))

	log := hivetest.Logger(t)
	require.NoError(t, h1.Start(log, context.TODO()))
	require.Equal(t, "foo", got1)
	require.Equal(t, 10, got2)
	require.NoError(t, h2.Start(log, context.TODO()))
	require.Equal(t, "bar", got1)
	require.Equal(t, 10, got2)

	require.NoError(t, h1.Stop(log, context.TODO()))
	require.NoError(t, h1.Stop(log, context.TODO()))
}

func TestModulePrivateProvidersDecorators(t *testing.T) {
	var intCalled bool
	opts := hive.DefaultOptions()
	opts.ModuleDecorators = cell.ModuleDecorators{
		func(n int) int { intCalled = true; return n + 1 },
		func(base string, mod cell.ModuleID) string { return base + string(mod) },
	}
	opts.ModulePrivateProviders = cell.ModulePrivateProviders{
		func() float64 { return 3.1415 },
	}

	var stringContents string
	var floatContents float64
	h := hive.NewWithOptions(opts,
		cell.Provide(
			// Things to decorate
			func() int { return 1 },
			func() string { return "hello, " },
		),
		cell.Module("test", "test",
			cell.Invoke(func(s string, f float64) {
				stringContents = s
				floatContents = f
			}),
		))

	require.NoError(t, h.Populate(hivetest.Logger(t)))
	require.Equal(t, "hello, test", stringContents)
	require.Equal(t, 3.1415, floatContents)
	require.False(t, intCalled, "did not expect unreferenced module decorator to be called")
}

// Test_Regression_Parallel_Config is a (-race) regression test for parallel use of cell.Config.
// pflag.FlagSet is keen on mutating things, even AddFlag() mutates the flag passed to it.
func Test_Regression_Parallel_Config(t *testing.T) {
	testCell := cell.Module(
		"test",
		"Test Module",
		cell.Config(Config{}),
	)
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()

			hive := hive.New(testCell)
			log := hivetest.Logger(t, hivetest.LogLevel(slog.LevelError))
			require.NoError(t, hive.Start(log, context.TODO()))
			require.NoError(t, hive.Stop(log, context.TODO()))
		}()
	}
	wg.Wait()
}
