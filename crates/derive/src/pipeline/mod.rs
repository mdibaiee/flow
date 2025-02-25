use super::combiner;
use super::registers;
use crate::{DocCounter, StatsAccumulator};

mod block;
mod invocation;
#[cfg(test)]
mod pipeline_test;

use block::{Block, BlockInvoke};
use futures::{stream::FuturesOrdered, StreamExt};
use invocation::{Invocation, InvokeOutput, InvokeStats};
use itertools::Itertools;
use protocol::{
    cgo, consumer,
    flow::{self, derive_api},
    message_flags,
};
use serde_json::Value;
use std::task::{Context, Poll};

#[derive(thiserror::Error, Debug, serde::Serialize)]
pub enum Error {
    #[error(transparent)]
    SchemaIndex(#[from] json::schema::index::Error),
    #[error("register error")]
    RegisterErr(#[from] registers::Error),
    #[error("lambda returned fewer rows than expected")]
    TooFewRows,
    #[error("lambda returned more rows than expected")]
    TooManyRows,
    #[error("derived document reduction error")]
    Combiner(#[from] combiner::Error),
    #[error("failed to open registers RocksDB")]
    #[serde(serialize_with = "crate::serialize_as_display")]
    Rocks(#[from] rocksdb::Error),
    #[error("invalid collection schema {:?}", .schema)]
    CollectionSchema {
        schema: String,
        #[source]
        #[serde(serialize_with = "crate::serialize_as_display")]
        source: url::ParseError,
    },
    #[error("invalid register schema {:?}", .schema)]
    RegisterSchema {
        schema: String,
        #[source]
        #[serde(serialize_with = "crate::serialize_as_display")]
        source: url::ParseError,
    },
    // TODO: Change these errors to use the JsonError so that they will include the original
    // document json.
    #[error("invalid register initial JSON")]
    #[serde(serialize_with = "crate::serialize_as_display")]
    RegisterJson(#[source] serde_json::Error),
    #[error("failed to invoke update lambda")]
    #[serde(serialize_with = "crate::serialize_as_display")]
    UpdateInvocationError(#[source] anyhow::Error),
    #[error("failed to invoke publish lambda")]
    #[serde(serialize_with = "crate::serialize_as_display")]
    PublishInvocationError(#[source] anyhow::Error),
    #[error("failed to parse lambda invocation response")]
    #[serde(serialize_with = "crate::serialize_as_display")]
    LambdaParseError(#[source] serde_json::Error),
}

pub struct Pipeline {
    // Collection being derived.
    collection: flow::CollectionSpec,
    // Transforms of the derivation.
    transforms: Vec<flow::TransformSpec>,
    // Schema against which registers must validate.
    registers_schema: url::Url,
    // Initial value of registers which have not yet been written.
    registers_initial: Value,
    // Models of update & publish Invocations for each transform. These are kept
    // pristine, and cloned to produce working instances given to new Blocks.
    updates_model: Vec<Invocation>,
    publishes_model: Vec<Invocation>,
    // Next Block currently being constructed.
    next: Block,
    // Trampoline used for lambda Invocations.
    trampoline: cgo::Trampoline,
    // Invocation futures awaiting completion of "update" lambdas.
    await_update: FuturesOrdered<BlockInvoke>,
    // Invocation futures awaiting completion of "publish" lambdas.
    await_publish: FuturesOrdered<BlockInvoke>,
    // Registers updated by "update" lambdas.
    // Pipeline owns Registers, but it's pragmatically public due to
    // unrelated usages in derive_api (clearing registers & checkpoints).
    registers: registers::Registers,
    // Combiner of derived documents.
    combiner: combiner::Combiner,
    // Partitions to extract when draining the Combiner.
    partitions: Vec<doc::Pointer>,
    // Validator for register validations.
    validator: doc::Validator<'static>,
    stats: PipelineStats,
}

/// Accumulates statistics about an individual transform within the pipeline.
#[derive(Default)]
struct TransformStats {
    input: DocCounter,
    update_lambda: InvokeStats,
    publish_lambda: InvokeStats,
}

impl StatsAccumulator for TransformStats {
    type Stats = derive_api::stats::TransformStats;

    fn drain(&mut self) -> Self::Stats {
        derive_api::stats::TransformStats {
            input: Some(self.input.drain()),
            update: Some(self.update_lambda.drain()),
            publish: Some(self.publish_lambda.drain()),
        }
    }
}

/// Accumulates statistics about each transform in the pipeline.
#[derive(Default)]
struct PipelineStats {
    transforms: Vec<TransformStats>,
}

impl StatsAccumulator for PipelineStats {
    type Stats = Vec<derive_api::stats::TransformStats>;

    fn drain(&mut self) -> Self::Stats {
        self.transforms.iter_mut().map(|t| t.drain()).collect()
    }
}

impl PipelineStats {
    fn transform_stats_mut(&mut self, transform_index: usize) -> &mut TransformStats {
        while self.transforms.len() <= transform_index {
            self.transforms.push(TransformStats::default());
        }
        &mut self.transforms[transform_index]
    }
}

impl Pipeline {
    pub fn from_config_and_parts(
        cfg: derive_api::Config,
        registers: registers::Registers,
        block_id: usize,
    ) -> Result<Self, Error> {
        let derive_api::Config {
            derivation,
            schema_index_memptr,
            ..
        } = cfg;

        // Re-hydrate a &'static SchemaIndex from a provided memory address.
        let schema_index_memptr = schema_index_memptr as usize;
        let schema_index: &doc::SchemaIndex = unsafe { std::mem::transmute(schema_index_memptr) };

        let flow::DerivationSpec {
            collection,
            transforms,
            register_initial_json,
            register_schema_uri,
            shard_template: _,
            recovery_log_template: _,
        } = derivation.unwrap_or_default();

        let collection = collection.unwrap_or_default();

        tracing::debug!(
            ?collection,
            ?register_initial_json,
            ?register_schema_uri,
            ?schema_index_memptr,
            ?transforms,
            "building from config"
        );

        // Build pristine "model" Invocations that we'll clone for new Blocks.
        let (updates_model, publishes_model): (Vec<_>, Vec<_>) = transforms
            .iter()
            .map(|tf| {
                (
                    Invocation::new(tf.update_lambda.as_ref()),
                    Invocation::new(tf.publish_lambda.as_ref()),
                )
            })
            .unzip();

        let first_block = Block::new(block_id, &updates_model, &publishes_model);

        // Build Combiner.
        let collection_schema =
            url::Url::parse(&collection.schema_uri).map_err(|source| Error::CollectionSchema {
                schema: collection.schema_uri.clone(),
                source,
            })?;
        let combiner = combiner::Combiner::new(
            collection_schema,
            collection
                .key_ptrs
                .iter()
                .map(|k| doc::Pointer::from_str(k))
                .collect::<Vec<_>>()
                .into(),
        );

        // Identify partitions to extract on combiner drain.
        let partitions = collection
            .projections
            .iter()
            // Projections are already sorted by field, but defensively sort again.
            .sorted_by_key(|proj| &proj.field)
            .filter_map(|proj| {
                if proj.is_partition_key {
                    Some(doc::Pointer::from_str(&proj.ptr))
                } else {
                    None
                }
            })
            .collect();

        let registers_schema =
            url::Url::parse(&register_schema_uri).map_err(|source| Error::RegisterSchema {
                schema: register_schema_uri.clone(),
                source,
            })?;
        let registers_initial =
            serde_json::from_str(&register_initial_json).map_err(Error::RegisterJson)?;
        let validator = doc::Validator::new(schema_index);

        Ok(Self {
            collection,
            transforms,
            registers_initial,
            registers_schema,
            updates_model,
            publishes_model,
            next: first_block,
            trampoline: cgo::Trampoline::new(),
            await_update: FuturesOrdered::new(),
            await_publish: FuturesOrdered::new(),
            registers,
            combiner,
            partitions,
            validator,
            stats: PipelineStats::default(),
        })
    }

    // Consume this Pipeline, returning its Registers and next Block ID.
    // This may only be called in between transactions, after draining.
    pub fn into_inner(self) -> (registers::Registers, usize) {
        assert_eq!(self.next.num_bytes, 0);
        assert!(self.trampoline.is_empty());

        (self.registers, self.next.id)
    }

    // Delegate to load the last persisted checkpoint.
    pub fn last_checkpoint(&self) -> Result<consumer::Checkpoint, registers::Error> {
        assert_eq!(self.next.num_bytes, 0);
        assert!(self.trampoline.is_empty());

        self.registers.last_checkpoint()
    }

    // Delegate to clear held registers.
    pub fn clear_registers(&mut self) -> Result<(), registers::Error> {
        assert_eq!(self.next.num_bytes, 0);
        assert!(self.trampoline.is_empty());

        self.registers.clear()
    }

    // Delegate to prepare the checkpoint for commit.
    pub fn prepare(
        &mut self,
        checkpoint: protocol::consumer::Checkpoint,
    ) -> Result<(), registers::Error> {
        assert_eq!(self.next.num_bytes, 0);
        assert!(self.trampoline.is_empty());

        self.registers.prepare(checkpoint)
    }

    // Resolve a trampoline task.
    pub fn resolve_task(&self, data: &[u8]) {
        self.trampoline.resolve_task(data)
    }

    // Add a source document to the Pipeline, and return true if it caused the
    // current Block to flush (and false otherwise).
    //
    // Currently this never returns an error, but it may in the future if an
    // invocation performed deeper processing of the input |body|
    // (e.x. by deserializing into Deno V8 types).
    pub fn add_source_document(
        &mut self,
        header: derive_api::DocHeader,
        body: &[u8],
    ) -> Result<bool, Error> {
        let derive_api::DocHeader {
            uuid,
            packed_key,
            transform_index,
        } = header;
        self.stats
            .transform_stats_mut(transform_index as usize)
            .input
            .increment(body.len() as u64);

        let uuid = uuid.unwrap_or_default();
        let flags = uuid.producer_and_flags & message_flags::MASK;

        if flags != message_flags::ACK_TXN {
            self.next
                .add_source(transform_index as usize, packed_key, body);
        }

        let dispatch =
            // Dispatch if we're over the size target.
            self.next.num_bytes >= BLOCK_SIZE_TARGET
            // Or if the block is non-empty and this document *isn't* a
            // continuation of an append transaction (e.x., where we can
            // expect a future ACK_TXN to be forthcoming), and there is
            // remaining concurrency.
            || (flags != message_flags::CONTINUE_TXN
                && self.await_update.len() < BLOCK_CONCURRENCY_TARGET
            );

        if dispatch {
            self.flush();
        }

        Ok(dispatch)
    }

    // Flush a partial Block to begin its processing.
    pub fn flush(&mut self) {
        if self.next.num_bytes == 0 {
            return;
        }
        let next = Block::new(self.next.id + 1, &self.updates_model, &self.publishes_model);
        let block = std::mem::replace(&mut self.next, next);
        self.await_update
            .push(block.invoke_updates(&self.trampoline));
    }

    // Poll pending Block invocations, processing all Blocks which immediately resolve.
    // Then, dispatch all started trampoline tasks to the provide vectors.
    pub fn poll_and_trampoline(
        &mut self,
        arena: &mut Vec<u8>,
        out: &mut Vec<cgo::Out>,
    ) -> Result<bool, Error> {
        let waker = futures::task::noop_waker();
        let mut ctx = Context::from_waker(&waker);

        // Process all ready blocks which were awaiting "update" lambda invocation.
        loop {
            match self.await_update.poll_next_unpin(&mut ctx) {
                Poll::Pending => break,
                Poll::Ready(None) => break,
                Poll::Ready(Some(result)) => {
                    let (mut block, update_outputs) =
                        result.map_err(Error::UpdateInvocationError)?;

                    for (transform_index, result) in update_outputs.iter().enumerate() {
                        self.stats
                            .transform_stats_mut(transform_index)
                            .update_lambda
                            .add(&result.stats);
                    }
                    self.update_registers(&mut block, update_outputs)?;

                    tracing::debug!(?block, "completed register updates, starting publishes");

                    self.await_publish
                        .push(block.invoke_publish(&self.trampoline));
                }
            }
        }

        // Process all ready blocks which were awaiting "publish" lambda invocation.
        loop {
            match self.await_publish.poll_next_unpin(&mut ctx) {
                Poll::Pending => break,
                Poll::Ready(None) => break,
                Poll::Ready(Some(result)) => {
                    let (mut block, publish_outputs) =
                        result.map_err(Error::PublishInvocationError)?;

                    for (transform_index, result) in publish_outputs.iter().enumerate() {
                        self.stats
                            .transform_stats_mut(transform_index)
                            .publish_lambda
                            .add(&result.stats);
                    }

                    self.combine_published(&mut block, publish_outputs)?;
                    tracing::debug!(?block, "completed publishes");
                }
            }
        }

        self.trampoline
            .dispatch_tasks(derive_api::Code::Trampoline as u32, arena, out);
        let idle = self.trampoline.is_empty();

        // Sanity check: the trampoline is empty / not empty only if
        // awaited futures are also empty / not empty.
        assert_eq!(
            idle,
            self.await_publish.is_empty() && self.await_update.is_empty()
        );

        Ok(idle)
    }

    // Drain the pipeline's combiner into the provide vectors.
    // This may be called only after polling to completion.
    pub fn drain(&mut self, arena: &mut Vec<u8>, out: &mut Vec<cgo::Out>) {
        assert_eq!(self.next.num_bytes, 0);
        assert!(self.trampoline.is_empty());

        let combine_out = crate::combine_api::drain_combiner(
            &mut self.combiner,
            &self.collection.uuid_ptr,
            &self.partitions,
            arena,
            out,
        );

        // Send a final message with the stats for this transaction.
        let stats = derive_api::Stats {
            output: Some(combine_out.into_stats()),
            registers: Some(self.registers.stats.drain()),
            transforms: self.stats.drain(),
        };
        cgo::send_message(derive_api::Code::Stats as u32, &stats, arena, out);
    }

    fn update_registers(
        &mut self,
        block: &mut Block,
        // tf_register_deltas is an array of reducible register deltas
        //  ... for each source document
        //  ... for each transform
        tf_register_deltas: Vec<InvokeOutput>,
    ) -> Result<(), Error> {
        // Load all registers in |keys|, so that we may read them below.
        self.registers.load(block.keys.iter())?;
        tracing::trace!(?block, registers = ?self.registers, "loaded registers");

        // Map into a vector of iterators over Vec<Value>.
        let mut tf_register_deltas = tf_register_deltas
            .into_iter()
            .map(|u| u.parsed.into_iter())
            .collect_vec();

        // Process documents in sequence, reducing the register updates of each
        // and accumulating register column buffers for future publish invocations.
        for (tf_ind, key) in block.transforms.iter().zip(block.keys.iter()) {
            let tf = &self.transforms[*tf_ind as usize];

            // If this transform has a update lambda, expect that we received zero or more
            // register deltas for this source document. Otherwise behave as if empty.
            let deltas = if tf.update_lambda.is_some() {
                tf_register_deltas[*tf_ind as usize]
                    .next()
                    .ok_or(Error::TooFewRows)?
            } else {
                Vec::new()
            };

            let publish = &mut block.publishes[*tf_ind as usize];
            publish.begin_register(self.registers.read(key, &self.registers_initial));

            // If we have deltas to apply, reduce them and assemble into
            // a future publish invocation body.
            if !deltas.is_empty() {
                self.registers.reduce(
                    key,
                    &self.registers_schema,
                    &self.registers_initial,
                    deltas.into_iter(),
                    &mut self.validator,
                )?;
                publish.end_register(Some(self.registers.read(key, &self.registers_initial)));
            } else {
                publish.end_register(None);
            }
        }
        tracing::trace!(?block, registers = ?self.registers, "reduced registers");

        // Verify that we precisely consumed expected outputs from each lambda.
        for mut it in tf_register_deltas {
            if let Some(_) = it.next() {
                return Err(Error::TooManyRows);
            }
        }

        Ok(())
    }

    fn combine_published(
        &mut self,
        block: &mut Block,
        // tf_derived_docs is an array of combined published documents
        //  ... for each source document
        //  ... for each transform
        tf_derived_docs: Vec<InvokeOutput>,
    ) -> Result<(), Error> {
        // Map into a vector of iterators over Vec<Value>.
        let mut tf_derived_docs = tf_derived_docs
            .into_iter()
            .map(|u| u.parsed.into_iter())
            .collect_vec();

        for tf_ind in &block.transforms {
            let tf = &self.transforms[*tf_ind as usize];

            // If this transform has a publish lambda, expect that we received zero or more
            // derived documents for this source document. Otherwise behave as if empty.
            let derived_docs = if tf.publish_lambda.is_some() {
                tf_derived_docs[*tf_ind as usize]
                    .next()
                    .ok_or(Error::TooFewRows)?
            } else {
                Vec::new()
            };

            for doc in derived_docs {
                self.combiner.combine_right(doc, &mut self.validator)?;
            }
        }
        tracing::trace!(combiner = ?self.combiner, "combined documents");

        // Verify that we precisely consumed expected outputs from each lambda.
        for mut it in tf_derived_docs {
            if let Some(_) = it.next() {
                return Err(Error::TooManyRows);
            }
        }

        Ok(())
    }
}

const BLOCK_SIZE_TARGET: usize = 1 << 16;
const BLOCK_CONCURRENCY_TARGET: usize = 3;

#[cfg(test)]
mod test {
    use super::*;
    use std::time::Duration;

    #[test]
    fn test_pipeline_stats() {
        let mut stats = PipelineStats::default();

        stats.transform_stats_mut(2).input.increment(42);
        stats.transform_stats_mut(2).input.increment(42);
        stats
            .transform_stats_mut(0)
            .update_lambda
            .add(&InvokeStats {
                output: DocCounter::new(5, 999),
                total_duration: Duration::from_secs(2),
            });
        stats
            .transform_stats_mut(0)
            .update_lambda
            .add(&InvokeStats {
                output: DocCounter::new(1, 1),
                total_duration: Duration::from_secs(1),
            });
        stats
            .transform_stats_mut(2)
            .publish_lambda
            .add(&InvokeStats {
                output: DocCounter::new(3, 8192),
                total_duration: Duration::from_secs(3),
            });

        let actual = stats.drain();
        insta::assert_yaml_snapshot!(actual);
    }
}
