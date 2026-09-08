<?php

use Spiral\Goridge\RPC\RPC;
use Spiral\Goridge\StreamRelay;
use Spiral\RoadRunner\Jobs\Consumer;
use Spiral\RoadRunner\Jobs\Jobs;
use Spiral\RoadRunner\Jobs\Queue\AMQPCreateInfo;

ini_set("display_errors", "stderr");
require dirname(__DIR__) . "/vendor/autoload.php";

$consumer = new Consumer();

while ($task = $consumer->waitTask()) {
    try {
        if ($task->getPipeline() !== "connections-source" || $task->getName() !== "connections.source") {
            throw new RuntimeException("Unexpected source task");
        }

        $stream = stream_socket_client("tcp://127.0.0.1:6002", $errorCode, $errorMessage, 5);
        if ($stream === false) {
            throw new RuntimeException($errorMessage, $errorCode);
        }
        stream_set_timeout($stream, 5);
        $jobs = new Jobs(new RPC(new StreamRelay($stream, $stream)));
        $destination = $jobs->create(new AMQPCreateInfo(
            name: "connections-destination",
            queue: "amqp-connections-queue",
            exchange: "amqp-connections-exchange",
            routingKey: "amqp-connections-route",
            queueHeaders: ["rr_connection" => "brokerB", "x-queue-mode" => "lazy"],
            deleteQueueOnStop: true,
        ));

        // Resume declares the broker queue. Pause leaves the forwarded task for Go.
        $jobs->resume($destination);
        $jobs->pause($destination);

        $destination->dispatch(
            $destination->create("connections.forward", $task->getPayload())
                ->withHeader("test", $task->getHeader("test"))
        );
        $task->ack();
    } catch (\Throwable $e) {
        $task->fail($e);
    }
}
