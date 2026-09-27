package repository

import (
	"context"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/model"
	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentedDB decorates DB adding metrics and tracing to every operation
type InstrumentedDB struct {
	*DB
	metrics *o11.RepoMetrics
	tracer  trace.Tracer
}

// NewInstrumented wraps a DB with repository-layer metrics and tracing
func NewInstrumented(db *DB, metrics *o11.RepoMetrics, tp trace.TracerProvider) *InstrumentedDB {
	return &InstrumentedDB{
		DB:      db,
		metrics: metrics,
		tracer:  tp.Tracer("repository/sqlite"),
	}
}

// record measures one repository operation
func (i *InstrumentedDB) record(ctx context.Context, operation string, start time.Time, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("result", result),
	)
	i.metrics.QueriesTotal.Add(ctx, 1, attrs)
	i.metrics.QueryDuration.Record(ctx, time.Since(start).Seconds(),
		metric.WithAttributes(attribute.String("operation", operation)))
}

// wrap executes op with span + metrics
func (i *InstrumentedDB) wrap(ctx context.Context, operation string, op func(ctx context.Context) error) error {
	start := time.Now()
	ctx, span := i.tracer.Start(ctx, "repo."+operation)
	defer span.End()

	err := op(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	i.record(ctx, operation, start, err)
	return err
}

func (i *InstrumentedDB) SaveSubscription(ctx context.Context, subscriptionText, user string) error {
	return i.wrap(ctx, "subscriptions.insert", func(ctx context.Context) error {
		return i.DB.SaveSubscription(ctx, subscriptionText, user)
	})
}

func (i *InstrumentedDB) DeleteSubscription(ctx context.Context, user string) error {
	return i.wrap(ctx, "subscriptions.delete", func(ctx context.Context) error {
		return i.DB.DeleteSubscription(ctx, user)
	})
}

func (i *InstrumentedDB) ReturnSubscriptions(ctx context.Context, user string) (subs []model.Subscription, err error) {
	err = i.wrap(ctx, "subscriptions.select", func(ctx context.Context) error {
		var err error
		subs, err = i.DB.ReturnSubscriptions(ctx, user)
		return err
	})
	return subs, err
}

func (i *InstrumentedDB) MatchSubscriptions(ctx context.Context, file string) (users []string, err error) {
	err = i.wrap(ctx, "subscriptions.match", func(ctx context.Context) error {
		var err error
		users, err = i.DB.MatchSubscriptions(ctx, file)
		return err
	})
	return users, err
}

func (i *InstrumentedDB) CountTokenUses(ctx context.Context, jwt string) (count int, err error) {
	err = i.wrap(ctx, "jwt_used.count", func(ctx context.Context) error {
		var err error
		count, err = i.DB.CountTokenUses(ctx, jwt)
		return err
	})
	return count, err
}

func (i *InstrumentedDB) SaveToken(ctx context.Context, jwt string) error {
	return i.wrap(ctx, "jwt_used.insert", func(ctx context.Context) error {
		return i.DB.SaveToken(ctx, jwt)
	})
}

func (i *InstrumentedDB) CleanJWT(ctx context.Context) error {
	return i.wrap(ctx, "jwt_used.clean", func(ctx context.Context) error {
		return i.DB.CleanJWT(ctx)
	})
}

func (i *InstrumentedDB) ReportFileUsage(ctx context.Context, file string, result int) error {
	return i.wrap(ctx, "file_usage.insert", func(ctx context.Context) error {
		return i.DB.ReportFileUsage(ctx, file, result)
	})
}

func (i *InstrumentedDB) ReportUserUsage(ctx context.Context, user, file string) error {
	return i.wrap(ctx, "user_usage.insert", func(ctx context.Context) error {
		return i.DB.ReportUserUsage(ctx, user, file)
	})
}
