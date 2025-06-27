package utils

import "sync"

type Subscription[T interface{}] struct {
	channel    chan T
	blocking   bool
	dispatcher *Dispatcher[T]
}

type Dispatcher[T interface{}] struct {
	mutex         sync.Mutex
	subscriptions []*Subscription[T]
}

func (d *Dispatcher[T]) Subscribe(capacity int, blocking bool) *Subscription[T] {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	subscription := &Subscription[T]{
		channel:    make(chan T, capacity),
		blocking:   blocking,
		dispatcher: d,
	}
	d.subscriptions = append(d.subscriptions, subscription)

	return subscription
}

func (s *Subscription[T]) Unsubscribe() {
	if s.dispatcher == nil {
		return
	}

	s.dispatcher.Unsubscribe(s)
}

func (s *Subscription[T]) Channel() <-chan T {
	return s.channel
}

func (d *Dispatcher[T]) Unsubscribe(subscription *Subscription[T]) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if subscription.dispatcher != nil {
		return
	}

	count := len(d.subscriptions)

	for i, s := range d.subscriptions {
		if s == subscription {
			if i < count-1 {
				d.subscriptions[i] = d.subscriptions[count-1]
			}

			d.subscriptions = d.subscriptions[:count-1]
			subscription.dispatcher = nil

			return
		}
	}
}

func (d *Dispatcher[T]) Fire(data T) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	for _, s := range d.subscriptions {
		if s.blocking {
			s.channel <- data
		} else {
			select {
			case s.channel <- data:
			default:
			}
		}
	}
}
