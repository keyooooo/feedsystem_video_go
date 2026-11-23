import { postJson } from './client'
import { normalizeCommentList } from './normalize'
import type { Comment, CommentLikeState, CommentPage, CommentReply, CommentThread, MessageResponse } from './types'

export async function listAll(videoId: number) {
  const comments = await postJson<Comment[] | null>('/comment/listAll', { video_id: videoId })
  return normalizeCommentList(comments)
}

export function listThreads(videoId: number, cursor = 0) {
  return postJson<CommentPage<CommentThread>>('/comment/listThreads', { video_id: videoId, cursor })
}

export function listReplies(videoId: number, rootId: number, cursor = 0) {
  return postJson<CommentPage<CommentReply>>('/comment/listReplies', { video_id: videoId, root_comment_id: rootId, cursor })
}

export function publish(videoId: number, content: string, replyTo?: number) {
  return postJson<MessageResponse & { comment?: CommentReply }>('/comment/publish', {
    video_id: videoId,
    content,
    reply_to_comment_id: replyTo,
  }, { authRequired: true })
}

export function remove(commentId: number) {
  return postJson<MessageResponse & { deleted_count?: number }>('/comment/delete', { comment_id: commentId }, { authRequired: true })
}

export function setLike(commentId: number, liked: boolean) {
  return postJson<CommentLikeState>('/comment/setLike', { comment_id: commentId, liked }, { authRequired: true })
}
